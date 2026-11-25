// Package nat installs per-address PREROUTING rules. A TCP 443 redirect, when
// requested, is appended before the DNAT for that address. The chain is never flushed.
package nat

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

const proxyPort = "8443"

var (
	stableNet = mustCIDR("10.254.253.0/24")
	tunnelNet = mustCIDR("10.254.254.0/30")
)

// Runner executes a command and returns its combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// Plan deletes existing PREROUTING rules for stable, highest line number first,
// then appends an optional TCP 443 redirect and the DNAT to dest.
// It never emits iptables -F, and it never redirects UDP 443.
// The stable address must be a host in 10.254.253.0/24. The destination must
// be a unicast address outside the stable subnet, the tunnel, and loopback.
func Plan(listOutput, stable, dest string, redirect bool) ([][]string, error) {
	stableIP, err := stableAddr(stable)
	if err != nil {
		return nil, err
	}
	destIP, err := ipv4(dest)
	if err != nil {
		return nil, fmt.Errorf("nat destination: %w", err)
	}
	if d := net.ParseIP(destIP); stableNet.Contains(d) || tunnelNet.Contains(d) || d.IsLoopback() ||
		d.IsUnspecified() || d.IsMulticast() || d.Equal(net.IPv4bcast) {
		return nil, fmt.Errorf("nat destination %s is not a container bridge address", destIP)
	}
	cmds := deletes(listOutput, stableIP)
	if redirect {
		cmds = append(cmds, []string{
			"iptables", "-t", "nat", "-A", "PREROUTING",
			"-i", "wg0", "-p", "tcp", "-d", stableIP, "--dport", "443",
			"-j", "REDIRECT", "--to-ports", proxyPort,
		})
	}
	cmds = append(cmds, []string{
		"iptables", "-t", "nat", "-A", "PREROUTING",
		"-i", "wg0", "-d", stableIP,
		"-j", "DNAT", "--to-destination", destIP,
	})
	return cmds, nil
}

// Install lists PREROUTING and applies Plan. It does not flush the chain.
func Install(ctx context.Context, run Runner, stable, dest string, redirect bool) error {
	out, err := run.Run(ctx, "iptables", "-t", "nat", "-L", "PREROUTING", "-n", "--line-numbers")
	if err != nil {
		return fmt.Errorf("list PREROUTING: %w", err)
	}
	cmds, err := Plan(out, stable, dest, redirect)
	if err != nil {
		return err
	}
	return apply(ctx, run, cmds)
}

// Remove deletes every PREROUTING rule for stable by line number, so a stopped
// container's old bridge address is not reached through a stale DNAT.
func Remove(ctx context.Context, run Runner, stable string) error {
	stableIP, err := stableAddr(stable)
	if err != nil {
		return err
	}
	out, err := run.Run(ctx, "iptables", "-t", "nat", "-L", "PREROUTING", "-n", "--line-numbers")
	if err != nil {
		return fmt.Errorf("list PREROUTING: %w", err)
	}
	return apply(ctx, run, deletes(out, stableIP))
}

func apply(ctx context.Context, run Runner, cmds [][]string) error {
	for _, cmd := range cmds {
		if _, err := run.Run(ctx, cmd[0], cmd[1:]...); err != nil {
			return fmt.Errorf("nat: %w", err)
		}
	}
	return nil
}

func deletes(listOutput, stableIP string) [][]string {
	lines := matchingLines(listOutput, stableIP)
	sort.Sort(sort.Reverse(sort.IntSlice(lines)))
	var cmds [][]string
	for _, n := range lines {
		cmds = append(cmds, []string{"iptables", "-t", "nat", "-D", "PREROUTING", strconv.Itoa(n)})
	}
	return cmds
}

func stableAddr(s string) (string, error) {
	ip, err := ipv4(s)
	if err != nil {
		return "", fmt.Errorf("nat stable address: %w", err)
	}
	p := net.ParseIP(ip).To4()
	if !stableNet.Contains(p) || p[3] == 0 || p[3] == 255 {
		return "", fmt.Errorf("nat stable address %s is outside %s", ip, stableNet)
	}
	return ip, nil
}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func ipv4(s string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("invalid IPv4 %q", s)
	}
	return ip.To4().String(), nil
}

func matchingLines(output, ip string) []int {
	var lines []int
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		n, ok := lineNumber(fields)
		if !ok || !matchesRule(fields, ip) {
			continue
		}
		lines = append(lines, n)
	}
	return lines
}

func lineNumber(fields []string) (int, bool) {
	if len(fields) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

func matchesRule(fields []string, ip string) bool {
	if len(fields) < 5 {
		return false
	}
	if fields[1] != "REDIRECT" && fields[1] != "DNAT" {
		return false
	}
	destIdx := 4
	if fields[3] == "--" || fields[3] == "-f" || fields[3] == "!f" {
		destIdx = 5
	}
	if destIdx >= len(fields) {
		return false
	}
	tok := strings.Trim(fields[destIdx], "[](),")
	return tok == ip || tok == ip+"/32"
}
