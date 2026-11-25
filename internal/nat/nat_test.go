package nat

import (
	"context"
	"strings"
	"testing"
)

const sample = `
Chain PREROUTING (policy ACCEPT)
num  target     prot opt source               destination
1    DOCKER     all  --  0.0.0.0/0            0.0.0.0/0            ADDRTYPE match dst-type LOCAL
2    REDIRECT   tcp  --  0.0.0.0/0            10.254.253.10        tcp dpt:443 redir ports 8443
3    DNAT       all  --  0.0.0.0/0            10.254.253.1         to:172.18.0.9
4    DNAT       all  --  0.0.0.0/0            10.254.253.10        to:172.18.0.3
5    DNAT       all  --  0.0.0.0/0            10.254.253.100       to:172.18.0.8
`

func TestRedirectBeforeDNAT(t *testing.T) {
	cmds, err := Plan(sample, "10.254.253.10", "172.18.0.4", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 4 {
		t.Fatalf("got %d commands", len(cmds))
	}
	if cmds[0][len(cmds[0])-1] != "4" || cmds[1][len(cmds[1])-1] != "2" {
		t.Fatalf("delete order %#v then %#v", cmds[0], cmds[1])
	}
	assertNoFlushOrUDP(t, cmds)
	redir := indexOf(cmds, "REDIRECT")
	dnat := indexOf(cmds, "DNAT")
	if redir < 0 || dnat < 0 || redir > dnat {
		t.Fatalf("redirect %d dnat %d", redir, dnat)
	}
	if !same(cmds[redir], []string{
		"iptables", "-t", "nat", "-A", "PREROUTING",
		"-i", "wg0", "-p", "tcp", "-d", "10.254.253.10", "--dport", "443",
		"-j", "REDIRECT", "--to-ports", "8443",
	}) {
		t.Fatalf("redirect %#v", cmds[redir])
	}
	if !same(cmds[dnat], []string{
		"iptables", "-t", "nat", "-A", "PREROUTING",
		"-i", "wg0", "-d", "10.254.253.10",
		"-j", "DNAT", "--to-destination", "172.18.0.4",
	}) {
		t.Fatalf("dnat %#v", cmds[dnat])
	}
}

func TestNoRedirectDeletesOldAndSkipsUDP(t *testing.T) {
	cmds, err := Plan(sample, "10.254.253.10", "172.18.0.4", false)
	if err != nil {
		t.Fatal(err)
	}
	assertNoFlushOrUDP(t, cmds)
	if indexOf(cmds, "REDIRECT") >= 0 {
		t.Fatalf("redirect installed: %#v", cmds)
	}
	if indexOf(cmds, "DNAT") != len(cmds)-1 {
		t.Fatalf("dnat is not last: %#v", cmds)
	}
}

func TestRejectBadIP(t *testing.T) {
	if _, err := Plan(sample, "not-an-ip", "172.18.0.1", false); err == nil {
		t.Fatal("accepted a bad stable address")
	}
	if _, err := Plan(sample, "10.254.253.10", "", true); err == nil {
		t.Fatal("accepted an empty destination")
	}
	for _, stable := range []string{"172.18.0.3", "10.254.254.2", "10.254.253.0", "10.254.253.255"} {
		if _, err := Plan(sample, stable, "172.18.0.4", false); err == nil {
			t.Fatalf("accepted stable %s outside the stable subnet", stable)
		}
	}
	for _, dest := range []string{"10.254.253.11", "10.254.254.1", "10.254.254.2", "127.0.0.1", "0.0.0.0", "224.0.0.1", "255.255.255.255"} {
		if _, err := Plan(sample, "10.254.253.10", dest, false); err == nil {
			t.Fatalf("accepted destination %s", dest)
		}
	}
}

func TestRemoveDeletesOnlyThatAddress(t *testing.T) {
	run := &fakeRunner{out: map[string]string{
		"iptables -t nat -L PREROUTING -n --line-numbers": sample,
	}}
	if err := Remove(context.Background(), run, "10.254.253.10"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"iptables", "-t", "nat", "-L", "PREROUTING", "-n", "--line-numbers"},
		{"iptables", "-t", "nat", "-D", "PREROUTING", "4"},
		{"iptables", "-t", "nat", "-D", "PREROUTING", "2"},
	}
	if len(run.calls) != len(want) {
		t.Fatalf("calls %#v", run.calls)
	}
	for i := range want {
		if !same(run.calls[i], want[i]) {
			t.Fatalf("call %d %#v want %#v", i, run.calls[i], want[i])
		}
	}
	assertNoFlushOrUDP(t, run.calls)
	if err := Remove(context.Background(), run, "172.18.0.3"); err == nil {
		t.Fatal("removed rules for a bridge address")
	}
}

func TestInstallUsesPlan(t *testing.T) {
	run := &fakeRunner{out: map[string]string{
		"iptables -t nat -L PREROUTING -n --line-numbers": sample,
	}}
	if err := Install(context.Background(), run, "10.254.253.10", "172.18.0.4", true); err != nil {
		t.Fatal(err)
	}
	if len(run.calls) != 5 {
		t.Fatalf("calls %d: %#v", len(run.calls), run.calls)
	}
	assertNoFlushOrUDP(t, run.calls)
}

func TestPlanMatchesDestinationAndIgnoresUnrelatedRules(t *testing.T) {
	nftSample := `
Chain PREROUTING (policy ACCEPT)
num  target     prot source               destination
1    DOCKER     all  0.0.0.0/0            10.254.253.10
2    DNAT       all  10.254.253.10        10.254.253.11        to:172.18.0.9
3    REDIRECT   tcp  0.0.0.0/0            10.254.253.10        tcp dpt:443 redir ports 8443
4    DNAT       all  0.0.0.0/0            10.254.253.10        to:172.18.0.3
`
	cmds, err := Plan(nftSample, "10.254.253.10", "172.18.0.4", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 3 {
		t.Fatalf("cmds %#v", cmds)
	}
	if !same(cmds[0], []string{"iptables", "-t", "nat", "-D", "PREROUTING", "4"}) ||
		!same(cmds[1], []string{"iptables", "-t", "nat", "-D", "PREROUTING", "3"}) {
		t.Fatalf("deletes %#v", cmds[:2])
	}
	assertNoFlushOrUDP(t, cmds)
}

func assertNoFlushOrUDP(t *testing.T, cmds [][]string) {
	t.Helper()
	for _, cmd := range cmds {
		for _, arg := range cmd {
			if arg == "-F" || arg == "--flush" || arg == "udp" || arg == "-p udp" {
				t.Fatalf("forbidden arg %q in %#v", arg, cmd)
			}
		}
	}
}

func indexOf(cmds [][]string, target string) int {
	for i, cmd := range cmds {
		for _, arg := range cmd {
			if arg == target {
				return i
			}
		}
	}
	return -1
}

func same(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type fakeRunner struct {
	calls [][]string
	out   map[string]string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := append([]string{name}, args...)
	f.calls = append(f.calls, cmd)
	return f.out[strings.Join(cmd, " ")], nil
}
