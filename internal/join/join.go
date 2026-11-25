// Package join attaches the controller to project networks and pins the
// default route on the whalias network afterwards.
package join

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	stableNet = "10.254.253.0/24"
	tunnelNet = "10.254.254.0/30"
	loopback  = "127.0.0.0/8"
)

var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix(stableNet),
	netip.MustParsePrefix(tunnelNet),
	netip.MustParsePrefix(loopback),
}

// Network is a candidate for attachment. ContainerIDs is ignored: network
// inspect omits stopped containers, and that is not a reason to leave.
type Network struct {
	ID           string
	Name         string
	Driver       string
	Subnet       string
	Subnets      []string
	Gateway      string
	ContainerIDs []string
}

// Event is a network event the manager understands.
type Event struct {
	Action     string
	ID         string
	Attributes map[string]string
}

// Connector attaches this container to a network. Disconnect is part of the
// interface so a test can see that the manager does not call it.
type Connector interface {
	Connect(ctx context.Context, networkID, containerID string) error
	Disconnect(ctx context.Context, networkID, containerID string) error
}

// Runner executes a command and returns its combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// OSRunner runs commands on the host.
type OSRunner struct{}

// Run implements Runner.
func (OSRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	return runCommand(ctx, name, args...)
}

// Manager remembers networks the user disconnected and does not rejoin them
// until that network is deleted and created again.
type Manager struct {
	selfID       string
	path         string
	client       Connector
	runner       Runner
	onJoined     func()
	mu           sync.Mutex
	homeVia      string
	homeDev      string
	disconnected map[string]struct{}
	joined       map[string]Network
}

// New loads the remembered disconnects from path.
func New(selfID, path string, client Connector, runner Runner) (*Manager, error) {
	m := &Manager{
		selfID:       selfID,
		path:         path,
		client:       client,
		runner:       runner,
		disconnected: map[string]struct{}{},
		joined:       map[string]Network{},
	}
	if path == "" {
		return m, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return m, nil
		}
		return nil, err
	}
	var state struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse disconnected networks: %w", err)
	}
	for _, id := range state.IDs {
		if id != "" {
			m.disconnected[id] = struct{}{}
		}
	}
	return m, nil
}

// OnJoined is called after a successful join, once the default route is back.
// The controller uses it to reapply rp_filter when the new interface appears.
func (m *Manager) OnJoined(fn func()) {
	m.mu.Lock()
	m.onJoined = fn
	m.mu.Unlock()
}

// IsSelf reports whether containerID is this controller.
func (m *Manager) IsSelf(containerID string) bool {
	return sameContainer(m.selfID, containerID)
}

// CaptureHome records the gateway and device of the whalias network. Later
// joins put the default route back there.
func (m *Manager) CaptureHome(ctx context.Context, gateway, selfIP string) error {
	if selfIP != "" && gateway != "" {
		out, err := m.runner.Run(ctx, "ip", "-4", "-o", "addr", "show")
		if err != nil {
			return err
		}
		if dev, ok := ParseDevice(out, selfIP); ok {
			m.mu.Lock()
			m.homeVia = gateway
			m.homeDev = dev
			m.mu.Unlock()
			return nil
		}
	}
	out, err := m.runner.Run(ctx, "ip", "route", "show", "default")
	if err != nil {
		return err
	}
	via, dev, ok := ParseDefault(out)
	if !ok {
		return errors.New("join: whalias default route not found")
	}
	m.mu.Lock()
	m.homeVia = via
	m.homeDev = dev
	m.mu.Unlock()
	return nil
}

// CheckHome verifies that the controller's own whalias network has valid IPv4
// subnet(s) that do not overlap the stable subnet, the tunnel, or loopback.
func CheckHome(n Network) error {
	subnets := networkSubnets(n)
	if len(subnets) == 0 {
		return fmt.Errorf("join: home network %s has no IPv4 subnet", n.Name)
	}
	for _, raw := range subnets {
		p, ok := parseIPv4Prefix(raw)
		if !ok {
			return fmt.Errorf("join: home network %s has invalid IPv4 subnet %q", n.Name, raw)
		}
		for _, r := range reservedPrefixes {
			if p.Overlaps(r) {
				return fmt.Errorf("join: home network %s subnet %s overlaps %s", n.Name, p, r)
			}
		}
	}
	return nil
}

// SkipReason explains why a network is not joined. An empty string means join.
// Overlap with the stable subnet, the tunnel, loopback, or an already joined
// network is refused. host, none, bridge, and whalias are refused by name.
// A network without parseable IPv4 subnets is refused, since overlap cannot
// be ruled out.
func SkipReason(n Network, joined []Network) string {
	switch n.Driver {
	case "host", "null":
		return "driver " + n.Driver
	}
	switch n.Name {
	case "host", "none", "bridge", "whalias":
		return "skipped network " + n.Name
	}
	subnets := networkSubnets(n)
	if len(subnets) == 0 {
		return fmt.Sprintf("no IPv4 subnet (%q)", n.Subnet)
	}
	prefixes := make([]netip.Prefix, 0, len(subnets))
	for _, raw := range subnets {
		p, ok := parseIPv4Prefix(raw)
		if !ok {
			return fmt.Sprintf("no IPv4 subnet (%q)", raw)
		}
		for _, r := range reservedPrefixes {
			if p.Overlaps(r) {
				return "overlaps " + r.String()
			}
		}
		prefixes = append(prefixes, p)
	}
	for _, other := range joined {
		if other.ID == n.ID {
			continue
		}
		for _, otherRaw := range networkSubnets(other) {
			q, ok := parseIPv4Prefix(otherRaw)
			if !ok {
				continue
			}
			for _, p := range prefixes {
				if p.Overlaps(q) {
					return fmt.Sprintf("overlaps joined network %s (%s)", other.Name, q)
				}
			}
		}
	}
	return ""
}

// IsJoined reports whether the controller is known to be attached to networkID.
func (m *Manager) IsJoined(networkID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.joined[networkID]
	return ok
}

// Join connects the controller to n and pins the default route. A remembered
// user disconnect is left alone. The project subnet is not installed as a route.
func (m *Manager) Join(ctx context.Context, n Network) error {
	m.mu.Lock()
	if _, left := m.disconnected[n.ID]; left {
		m.mu.Unlock()
		return nil
	}
	reason := SkipReason(n, m.subnetListLocked(n.ID))
	hook := m.onJoined
	m.mu.Unlock()
	if reason != "" {
		log.Printf("whalias: skip network %s: %s", n.Name, reason)
		return nil
	}
	if err := m.client.Connect(ctx, n.ID, m.selfID); err != nil && !alreadyConnected(err) {
		return err
	}
	if err := m.restore(ctx); err != nil {
		return err
	}
	if hook != nil {
		hook()
	}
	m.mu.Lock()
	m.joined[n.ID] = n
	m.mu.Unlock()
	return nil
}

// Reconcile joins networks that exist and that the user has not disconnected.
// It never disconnects. An empty ContainerIDs list does not remove a join.
func (m *Manager) Reconcile(ctx context.Context, networks []Network, attached map[string]bool) error {
	sort.Slice(networks, func(i, j int) bool { return networks[i].Name < networks[j].Name })
	nextJoined := make(map[string]Network, len(attached))
	for _, n := range networks {
		if attached[n.ID] {
			nextJoined[n.ID] = n
		}
		_ = n.ContainerIDs
	}
	m.mu.Lock()
	m.joined = nextJoined
	m.mu.Unlock()
	var errs []error
	for _, n := range networks {
		if attached[n.ID] {
			continue
		}
		if err := m.Join(ctx, n); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n.Name, err))
		}
	}
	if err := m.restore(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Handle applies one network event. inspect is used for create and manual connect.
func (m *Manager) Handle(ctx context.Context, ev Event, inspect func(context.Context, string) (Network, error)) error {
	switch ev.Action {
	case "create":
		if inspect == nil {
			return errors.New("join: network inspect is nil")
		}
		n, err := inspect(ctx, ev.ID)
		if err != nil {
			return err
		}
		if n.ID == "" {
			n.ID = ev.ID
		}
		return m.Join(ctx, n)
	case "destroy":
		return m.Forget(ctx, ev.ID)
	case "disconnect":
		if m.IsSelf(ev.Attributes["container"]) {
			return m.Remember(ctx, ev.ID)
		}
		return nil
	case "connect":
		if m.IsSelf(ev.Attributes["container"]) {
			return m.noteSelfConnect(ctx, ev.ID, inspect)
		}
		return nil
	default:
		return nil
	}
}

// Remember records a user disconnect and does not rejoin that network ID.
func (m *Manager) Remember(ctx context.Context, networkID string) error {
	if networkID == "" {
		return nil
	}
	m.mu.Lock()
	m.disconnected[networkID] = struct{}{}
	delete(m.joined, networkID)
	err := m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return m.restore(ctx)
}

// Forget drops a deleted network so a later create of a new ID can join.
func (m *Manager) Forget(ctx context.Context, networkID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.disconnected, networkID)
	delete(m.joined, networkID)
	return m.saveLocked()
}

func (m *Manager) noteSelfConnect(ctx context.Context, networkID string, inspect func(context.Context, string) (Network, error)) error {
	m.mu.Lock()
	delete(m.disconnected, networkID)
	_, alreadyJoined := m.joined[networkID]
	hook := m.onJoined
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	if !alreadyJoined && networkID != "" && inspect != nil {
		if n, err := inspect(ctx, networkID); err == nil {
			if n.ID == "" {
				n.ID = networkID
			}
			m.mu.Lock()
			m.joined[networkID] = n
			m.mu.Unlock()
		}
	}
	if err := m.restore(ctx); err != nil {
		return err
	}
	if !alreadyJoined && hook != nil {
		hook()
	}
	return nil
}

func (m *Manager) restore(ctx context.Context) error {
	m.mu.Lock()
	via, dev := m.homeVia, m.homeDev
	m.mu.Unlock()
	if via == "" || dev == "" {
		return errors.New("join: home route was not captured")
	}
	_, err := m.runner.Run(ctx, "ip", "route", "replace", "default", "via", via, "dev", dev)
	if err != nil {
		return fmt.Errorf("restore default route: %w", err)
	}
	return nil
}

func (m *Manager) subnetListLocked(except string) []Network {
	out := make([]Network, 0, len(m.joined))
	for id, n := range m.joined {
		if id == except {
			continue
		}
		out = append(out, n)
	}
	return out
}

func (m *Manager) saveLocked() error {
	if m.path == "" {
		return nil
	}
	ids := make([]string, 0, len(m.disconnected))
	for id := range m.disconnected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	data, err := json.MarshalIndent(struct {
		IDs []string `json:"ids"`
	}{IDs: ids}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(m.path), "."+filepath.Base(m.path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.path); err != nil {
		return err
	}
	ok = true
	return nil
}

// ParseDevice finds the interface that holds ip in `ip -4 -o addr` output.
func ParseDevice(addrOut, ip string) (string, bool) {
	for _, line := range strings.Split(addrOut, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[2] != "inet" {
			continue
		}
		got := strings.Split(f[3], "/")[0]
		if got == ip {
			return strings.Split(f[1], "@")[0], true
		}
	}
	return "", false
}

// ParseDefault reads `ip route show default`.
func ParseDefault(out string) (via, dev string, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 5 && f[0] == "default" && f[1] == "via" && f[3] == "dev" {
			return f[2], f[4], true
		}
	}
	return "", "", false
}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, text)
	}
	return text, nil
}

func networkSubnets(n Network) []string {
	if len(n.Subnets) > 0 {
		return n.Subnets
	}
	if n.Subnet != "" {
		return []string{n.Subnet}
	}
	return nil
}

func parseIPv4Prefix(s string) (netip.Prefix, bool) {
	p, err := netip.ParsePrefix(strings.TrimSpace(s))
	if err != nil || !p.Addr().Is4() {
		return netip.Prefix{}, false
	}
	return p.Masked(), true
}

func alreadyConnected(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "already exists") || strings.Contains(s, "already connected")
}

func sameContainer(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	if len(a) >= 12 && len(b) >= 12 && (strings.HasPrefix(a, b) || strings.HasPrefix(b, a)) {
		return true
	}
	return false
}
