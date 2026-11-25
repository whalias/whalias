package join

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkipNamesDriversAndOverlap(t *testing.T) {
	for _, name := range []string{"host", "none", "bridge", "whalias"} {
		if SkipReason(Network{ID: "1", Name: name, Driver: "bridge", Subnet: "172.20.0.0/16"}, nil) == "" {
			t.Fatalf("joined %s", name)
		}
	}
	if SkipReason(Network{ID: "1", Name: "custom", Driver: "host"}, nil) == "" {
		t.Fatal("joined driver host")
	}
	if SkipReason(Network{ID: "1", Name: "custom", Driver: "null"}, nil) == "" {
		t.Fatal("joined driver null")
	}
	if SkipReason(Network{ID: "1", Name: "internal", Driver: "bridge", Subnet: "172.21.0.0/16"}, nil) != "" {
		t.Fatal("refused an internal project network")
	}
	cases := []Network{
		{ID: "a", Name: "stable", Subnet: "10.254.253.0/24"},
		{ID: "b", Name: "tunnel", Subnet: "10.254.254.2/32"},
		{ID: "c", Name: "loop", Subnet: "127.0.0.0/8"},
	}
	for _, n := range cases {
		if SkipReason(n, nil) == "" {
			t.Fatalf("joined overlapping %s", n.Subnet)
		}
	}
	joined := []Network{{ID: "have", Name: "one", Subnet: "172.18.0.0/16"}}
	if reason := SkipReason(Network{ID: "two", Name: "two", Subnet: "172.18.5.0/24"}, joined); reason == "" {
		t.Fatal("joined a second overlapping subnet")
	}
}

func TestUnknownSubnetIsNotJoined(t *testing.T) {
	for _, subnet := range []string{"", "garbage", "fd00::/64", "::ffff:10.254.253.0/120", "0.0.0.0/0"} {
		if SkipReason(Network{ID: "x", Name: "proj", Driver: "bridge", Subnet: subnet}, nil) == "" {
			t.Fatalf("joined a network with subnet %q", subnet)
		}
	}
	multiBad := Network{
		ID:      "m",
		Name:    "proj-multi",
		Driver:  "bridge",
		Subnet:  "172.22.0.0/16",
		Subnets: []string{"172.22.0.0/16", "10.254.253.0/24"},
	}
	if SkipReason(multiBad, nil) == "" {
		t.Fatal("joined a multi-subnet network whose second subnet overlaps the stable subnet")
	}
	joinedMulti := []Network{{ID: "j", Name: "joined", Subnets: []string{"172.23.0.0/16", "172.24.0.0/16"}}}
	if SkipReason(Network{ID: "k", Name: "other", Subnet: "172.24.1.0/24"}, joinedMulti) == "" {
		t.Fatal("joined a network overlapping the second subnet of a joined network")
	}
	run := &script{}
	client := &fakeConn{}
	m := manager(t, run, client)
	if err := m.CaptureHome(context.Background(), "172.18.0.1", "172.18.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := m.Join(context.Background(), Network{ID: "x", Name: "proj", Driver: "bridge"}); err != nil {
		t.Fatal(err)
	}
	if len(client.connects) != 0 || m.IsJoined("x") {
		t.Fatalf("connected without a subnet: %#v", client.connects)
	}
}

func TestCheckHomeRefusesOverlapAndEmpty(t *testing.T) {
	for _, n := range []Network{
		{Name: "whalias"},
		{Name: "whalias", Subnet: "fd00::/64"},
		{Name: "whalias", Subnet: "10.254.253.0/24"},
		{Name: "whalias", Subnet: "10.254.254.0/30"},
		{Name: "whalias", Subnet: "127.0.0.0/8"},
		{Name: "whalias", Subnets: []string{"172.18.0.0/16", "10.254.253.128/25"}},
	} {
		if err := CheckHome(n); err == nil {
			t.Fatalf("accepted invalid home network %#v", n)
		}
	}
	if err := CheckHome(Network{Name: "whalias", Subnet: "172.19.0.0/16"}); err != nil {
		t.Fatalf("rejected valid home network: %v", err)
	}
}

func TestReconcilePrunesDetachedNetworksAndSelfConnectUpdatesJoined(t *testing.T) {
	run := &script{}
	client := &fakeConn{}
	m := manager(t, run, client)
	if err := m.CaptureHome(context.Background(), "172.18.0.1", "172.18.0.2"); err != nil {
		t.Fatal(err)
	}
	proj := Network{ID: "p1", Name: "proj1", Driver: "bridge", Subnet: "172.25.0.0/16"}
	if err := m.Join(context.Background(), proj); err != nil {
		t.Fatal(err)
	}
	if !m.IsJoined("p1") {
		t.Fatal("p1 not joined")
	}
	// If p1 disappeared while events were dropped, Reconcile must prune it from joined
	// so IsJoined becomes false and a new network reusing its subnet can be joined.
	proj2 := Network{ID: "p2", Name: "proj2", Driver: "bridge", Subnet: "172.25.0.0/16"}
	if err := m.Reconcile(context.Background(), []Network{proj2}, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if m.IsJoined("p1") {
		t.Fatal("stale p1 remained joined after Reconcile")
	}
	if !m.IsJoined("p2") {
		t.Fatal("p2 was not joined after p1 was pruned")
	}
	// Manual self connect updates joined and fires the rp_filter hook.
	hooks := 0
	m.OnJoined(func() { hooks++ })
	ev := Event{Action: "connect", ID: "manual", Attributes: map[string]string{"container": "self"}}
	if err := m.Handle(context.Background(), ev, func(context.Context, string) (Network, error) {
		return Network{ID: "manual", Name: "manual", Driver: "bridge", Subnet: "172.26.0.0/16"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if !m.IsJoined("manual") {
		t.Fatal("manual self-connect not recorded in IsJoined")
	}
	if hooks != 1 {
		t.Fatalf("manual self-connect hook count %d, want 1", hooks)
	}
}

func TestIsJoinedTracksJoinAndUserDisconnect(t *testing.T) {
	run := &script{}
	client := &fakeConn{}
	m := manager(t, run, client)
	if err := m.CaptureHome(context.Background(), "172.18.0.1", "172.18.0.2"); err != nil {
		t.Fatal(err)
	}
	if m.IsJoined("proj") {
		t.Fatal("joined before Join")
	}
	if err := m.Join(context.Background(), Network{ID: "proj", Name: "proj", Subnet: "172.20.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	if !m.IsJoined("proj") {
		t.Fatal("not joined after Join")
	}
	ev := Event{Action: "disconnect", ID: "proj", Attributes: map[string]string{"container": "self"}}
	if err := m.Handle(context.Background(), ev, nil); err != nil {
		t.Fatal(err)
	}
	if m.IsJoined("proj") {
		t.Fatal("still joined after a user disconnect")
	}
}

func TestJoinRestoresHomeRouteAndNotProjectSubnet(t *testing.T) {
	run := &script{}
	client := &fakeConn{}
	m := manager(t, run, client)
	if err := m.CaptureHome(context.Background(), "172.18.0.1", "172.18.0.2"); err != nil {
		t.Fatal(err)
	}
	hooks := 0
	m.OnJoined(func() { hooks++ })
	n := Network{ID: "proj", Name: "proj_default", Driver: "bridge", Subnet: "172.20.0.0/16"}
	if err := m.Join(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if len(client.connects) != 1 || client.connects[0] != "proj" {
		t.Fatalf("connects %#v", client.connects)
	}
	if len(client.disconnects) != 0 {
		t.Fatalf("disconnected %#v", client.disconnects)
	}
	if hooks != 1 {
		t.Fatalf("rp_filter hook %d", hooks)
	}
	if !run.has("ip route replace default via 172.18.0.1 dev eth0") {
		t.Fatalf("route calls %#v", run.calls)
	}
	for _, call := range run.calls {
		if strings.Contains(call, "172.20.0.0/16") {
			t.Fatalf("routed the project subnet: %s", call)
		}
	}
}

func TestAlreadyConnectedStillRestores(t *testing.T) {
	run := &script{}
	client := &fakeConn{err: errors.New("endpoint already exists in network proj")}
	m := manager(t, run, client)
	if err := m.CaptureHome(context.Background(), "172.18.0.1", "172.18.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := m.Join(context.Background(), Network{ID: "proj", Name: "proj", Subnet: "172.20.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	if !run.has("ip route replace default via 172.18.0.1 dev eth0") {
		t.Fatalf("calls %#v", run.calls)
	}
}

func TestUserDisconnectIsRememberedUntilCreate(t *testing.T) {
	run := &script{}
	client := &fakeConn{}
	path := filepath.Join(t.TempDir(), "left.json")
	m, err := New("self", path, client, run)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CaptureHome(context.Background(), "172.18.0.1", "172.18.0.2"); err != nil {
		t.Fatal(err)
	}
	ev := Event{Action: "disconnect", ID: "old", Attributes: map[string]string{"container": "self"}}
	if err := m.Handle(context.Background(), ev, nil); err != nil {
		t.Fatal(err)
	}
	n := Network{ID: "old", Name: "proj", Subnet: "172.20.0.0/16", ContainerIDs: nil}
	if err := m.Reconcile(context.Background(), []Network{n}, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if len(client.connects) != 0 || len(client.disconnects) != 0 {
		t.Fatalf("connect %#v disconnect %#v", client.connects, client.disconnects)
	}
	reloaded, err := New("self", path, client, run)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.CaptureHome(context.Background(), "172.18.0.1", "172.18.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Reconcile(context.Background(), []Network{n}, nil); err != nil {
		t.Fatal(err)
	}
	if len(client.connects) != 0 {
		t.Fatal("reloaded manager reconnected a user disconnect")
	}
	if err := reloaded.Handle(context.Background(), Event{Action: "destroy", ID: "old"}, nil); err != nil {
		t.Fatal(err)
	}
	created := Network{ID: "new", Name: "proj", Subnet: "172.20.0.0/16"}
	if err := reloaded.Handle(context.Background(), Event{Action: "create", ID: "new"}, func(context.Context, string) (Network, error) {
		return created, nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(client.connects) != 1 || client.connects[0] != "new" {
		t.Fatalf("create again %#v", client.connects)
	}
}

func TestInspectOmissionDoesNotDisconnect(t *testing.T) {
	run := &script{}
	client := &fakeConn{}
	m := manager(t, run, client)
	if err := m.CaptureHome(context.Background(), "172.18.0.1", "172.18.0.2"); err != nil {
		t.Fatal(err)
	}
	n := Network{ID: "proj", Name: "proj", Subnet: "172.20.0.0/16", ContainerIDs: nil}
	if err := m.Reconcile(context.Background(), []Network{n}, map[string]bool{"proj": true}); err != nil {
		t.Fatal(err)
	}
	if len(client.disconnects) != 0 {
		t.Fatalf("left because inspect was empty: %#v", client.disconnects)
	}
	if len(client.connects) != 0 {
		t.Fatalf("reconnect of an attached network: %#v", client.connects)
	}
}

func TestOtherContainerDisconnectIsIgnored(t *testing.T) {
	run := &script{}
	client := &fakeConn{}
	m := manager(t, run, client)
	if err := m.CaptureHome(context.Background(), "172.18.0.1", "172.18.0.2"); err != nil {
		t.Fatal(err)
	}
	ev := Event{Action: "disconnect", ID: "proj", Attributes: map[string]string{"container": "someone-else"}}
	if err := m.Handle(context.Background(), ev, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.Join(context.Background(), Network{ID: "proj", Name: "proj", Subnet: "172.20.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	if len(client.connects) != 1 {
		t.Fatalf("connects %#v", client.connects)
	}
}

func TestParseRoute(t *testing.T) {
	dev, ok := ParseDevice("2: eth0    inet 172.18.0.2/16 brd 172.18.255.255 scope global eth0\n", "172.18.0.2")
	if !ok || dev != "eth0" {
		t.Fatalf("dev %s %v", dev, ok)
	}
	via, dev, ok := ParseDefault("default via 172.19.0.1 dev eth1\n")
	if !ok || via != "172.19.0.1" || dev != "eth1" {
		t.Fatalf("default %s %s %v", via, dev, ok)
	}
}

func manager(t *testing.T, run Runner, client Connector) *Manager {
	t.Helper()
	m, err := New("self", filepath.Join(t.TempDir(), "left.json"), client, run)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type fakeConn struct {
	connects    []string
	disconnects []string
	err         error
}

func (f *fakeConn) Connect(ctx context.Context, networkID, containerID string) error {
	f.connects = append(f.connects, networkID)
	return f.err
}

func (f *fakeConn) Disconnect(ctx context.Context, networkID, containerID string) error {
	f.disconnects = append(f.disconnects, networkID)
	return nil
}

type script struct {
	calls []string
}

func (s *script) Run(ctx context.Context, name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	s.calls = append(s.calls, line)
	if strings.Contains(line, "addr") {
		return "2: eth0    inet 172.18.0.2/16 brd 172.18.255.255 scope global eth0\n", nil
	}
	if strings.Contains(line, "route show default") {
		return "default via 172.19.0.1 dev eth1\n", nil
	}
	return "", nil
}

func (s *script) has(want string) bool {
	for _, call := range s.calls {
		if call == want {
			return true
		}
	}
	return false
}
