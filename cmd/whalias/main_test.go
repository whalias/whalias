package main

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whalias/whalias/internal/address"
	"github.com/whalias/whalias/internal/docker"
	"github.com/whalias/whalias/internal/join"
	"github.com/whalias/whalias/internal/proxy"
	"github.com/whalias/whalias/internal/wg"
)

func TestProxyListensOnTunnelAddress(t *testing.T) {
	host, port, err := net.SplitHostPort(proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSuffix(wg.Address, "/32"); host != want {
		t.Fatalf("proxy host %s, want wg0 address %s", host, want)
	}
	if port != "8443" {
		t.Fatalf("proxy port %s", port)
	}
}

func TestBridgeIPUsesJoinedProjectNetworksOnly(t *testing.T) {
	ctr := docker.Container{Networks: map[string]docker.Endpoint{
		"a_unjoined": {NetworkID: "u", IP: "172.30.0.5"},
		"b_stable":   {NetworkID: "s", IP: "10.254.253.9"},
		"c_zero":     {NetworkID: "z", IP: "0.0.0.0"},
		"d_mcast":    {NetworkID: "m", IP: "224.0.0.1"},
		"bridge":     {NetworkID: "d", IP: "172.17.0.2"},
		"whalias":    {NetworkID: "w", IP: "172.19.0.3"},
		"z_project":  {NetworkID: "p", IP: "172.20.0.4"},
	}}
	joined := map[string]bool{"s": true, "z": true, "m": true, "d": true, "w": true, "p": true}
	if got := bridgeIP(ctr, func(id string) bool { return joined[id] }); got != "172.20.0.4" {
		t.Fatalf("bridge %q", got)
	}
	ctr.State = "exited"
	if got := bridgeIP(ctr, func(id string) bool { return joined[id] }); got != "" {
		t.Fatalf("bridge %q for exited container", got)
	}
	ctr.State = "running"
	delete(joined, "p")
	if got := bridgeIP(ctr, func(id string) bool { return joined[id] }); got != "" {
		t.Fatalf("bridge %q without a joined project network", got)
	}
}

func TestDestroyClearsNATAndProxyKeepsDNS(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	book, err := address.Open(filepath.Join(dir, "addresses.json"))
	if err != nil {
		t.Fatal(err)
	}
	joinRun := &fakeJoinRunner{}
	conn := &fakeConnector{}
	mgr, err := join.New("self-id", filepath.Join(dir, "left.json"), conn, joinRun)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.CaptureHome(ctx, "172.19.0.1", "172.19.0.2"); err != nil {
		t.Fatal(err)
	}
	api := &fakeDocker{
		self: docker.Container{
			ID:   "self-id",
			Name: "whalias",
			Networks: map[string]docker.Endpoint{
				"whalias": {NetworkID: "net-home", IP: "172.19.0.2", Gateway: "172.19.0.1"},
				"proj":    {NetworkID: "net-proj", IP: "172.20.0.2"},
			},
		},
		networks: []docker.Network{
			{ID: "net-home", Name: "whalias", Driver: "bridge", Subnet: "172.19.0.0/16", Gateway: "172.19.0.1"},
			{ID: "net-proj", Name: "proj", Driver: "bridge", Subnet: "172.20.0.0/16"},
		},
		containers: map[string]docker.Container{
			"web-1": {
				ID:    "web-1",
				Name:  "web",
				State: "running",
				Networks: map[string]docker.Endpoint{
					"proj": {NetworkID: "net-proj", IP: "172.20.0.5"},
				},
			},
		},
	}
	natRun := &fakeNATRunner{}
	ctrl := &controller{
		book:   book,
		cli:    api,
		natRun: natRun,
		join:   mgr,
		selfID: "self-id",
		probe: func(context.Context, string, string) (proxy.Decision, error) {
			return proxy.Decision{Redirect: true, HTTPPort: 80}, nil
		},
	}

	ctrl.reconcile(ctx)
	ips, ok := book.Records("web.wha.internal")
	if !ok || len(ips) != 1 || ips[0].String() != "10.254.253.1" {
		t.Fatalf("records %v ok=%v", ips, ok)
	}
	if _, bridge, port, ok := book.Lookup(ips[0]); !ok || bridge != "172.20.0.5" || port != 80 {
		t.Fatalf("lookup bridge=%q port=%d ok=%v", bridge, port, ok)
	}

	// Simulate docker rm -f web: container disappears and destroy event arrives.
	delete(api.containers, "web-1")
	natRun.listOut = "Chain PREROUTING (policy ACCEPT)\n" +
		"num  target     prot opt source               destination\n" +
		"1    REDIRECT   tcp  --  0.0.0.0/0            10.254.253.1         tcp dpt:443 redir ports 8443\n" +
		"2    DNAT       all  --  0.0.0.0/0            10.254.253.1         to:172.20.0.5\n"
	natRun.calls = nil
	ctrl.handle(ctx, docker.Event{Type: "container", Action: "destroy", ID: "web-1"})

	if _, _, _, ok := book.Lookup(ips[0]); ok {
		t.Fatal("proxy target remained set after container destroy")
	}
	if kept, ok := book.Records("web.wha.internal"); !ok || len(kept) != 1 || kept[0].String() != "10.254.253.1" {
		t.Fatalf("DNS record did not survive destroy: %v ok=%v", kept, ok)
	}
	if !natRun.has("iptables -t nat -D PREROUTING 2") || !natRun.has("iptables -t nat -D PREROUTING 1") {
		t.Fatalf("NAT rules were not deleted by line number: %#v", natRun.calls)
	}
}

func TestSelfDisconnectClearsContainerNATAndFailedSelfInspectSkipsReconcile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	book, err := address.Open(filepath.Join(dir, "addresses.json"))
	if err != nil {
		t.Fatal(err)
	}
	joinRun := &fakeJoinRunner{}
	conn := &fakeConnector{}
	mgr, err := join.New("self-id", filepath.Join(dir, "left.json"), conn, joinRun)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.CaptureHome(ctx, "172.19.0.1", "172.19.0.2"); err != nil {
		t.Fatal(err)
	}
	api := &fakeDocker{
		self: docker.Container{
			ID:   "self-id",
			Name: "whalias",
			Networks: map[string]docker.Endpoint{
				"whalias": {NetworkID: "net-home", IP: "172.19.0.2", Gateway: "172.19.0.1"},
				"proj":    {NetworkID: "net-proj", IP: "172.20.0.2"},
			},
		},
		networks: []docker.Network{
			{ID: "net-home", Name: "whalias", Driver: "bridge", Subnet: "172.19.0.0/16", Gateway: "172.19.0.1"},
			{ID: "net-proj", Name: "proj", Driver: "bridge", Subnet: "172.20.0.0/16"},
			{ID: "net-new", Name: "proj2", Driver: "bridge", Subnet: "172.21.0.0/16"},
		},
		containers: map[string]docker.Container{
			"web-1": {
				ID:    "web-1",
				Name:  "web",
				State: "running",
				Networks: map[string]docker.Endpoint{
					"proj": {NetworkID: "net-proj", IP: "172.20.0.5"},
				},
			},
		},
	}
	natRun := &fakeNATRunner{}
	ctrl := &controller{
		book:   book,
		cli:    api,
		natRun: natRun,
		join:   mgr,
		selfID: "self-id",
		probe: func(context.Context, string, string) (proxy.Decision, error) {
			return proxy.Decision{Redirect: true, HTTPPort: 80}, nil
		},
	}

	// When inspecting self fails on reconcile, join nothing and leave containers untouched.
	api.selfErr = errors.New("docker inspect failed")
	ctrl.reconcile(ctx)
	if len(conn.connects) != 0 {
		t.Fatalf("joined networks after failed self inspect: %#v", conn.connects)
	}
	if len(book.Entries()) != 0 {
		t.Fatalf("synced containers after failed self inspect: %#v", book.Entries())
	}

	// Succeed reconcile, then simulate user disconnecting whalias from net-proj.
	api.selfErr = nil
	ctrl.reconcile(ctx)
	ips, ok := book.Records("web.wha.internal")
	if !ok || len(ips) != 1 {
		t.Fatalf("records %v ok=%v", ips, ok)
	}
	if _, _, _, ok := book.Lookup(ips[0]); !ok {
		t.Fatal("proxy target missing before disconnect")
	}

	natRun.listOut = "Chain PREROUTING (policy ACCEPT)\n" +
		"num  target     prot opt source               destination\n" +
		"1    DNAT       all  --  0.0.0.0/0            10.254.253.1         to:172.20.0.5\n"
	natRun.calls = nil
	ctrl.handle(ctx, docker.Event{
		Type:       "network",
		Action:     "disconnect",
		ID:         "net-proj",
		Attributes: map[string]string{"container": "self-id"},
	})
	if _, _, _, ok := book.Lookup(ips[0]); ok {
		t.Fatal("proxy target remained set after controller disconnected from proj")
	}
	if !natRun.has("iptables -t nat -D PREROUTING 1") {
		t.Fatalf("NAT rule was not removed on self disconnect: %#v", natRun.calls)
	}
	if kept, ok := book.Records("web.wha.internal"); !ok || len(kept) != 1 {
		t.Fatalf("DNS record dropped on self disconnect: %v ok=%v", kept, ok)
	}
}

type fakeDocker struct {
	self       docker.Container
	selfErr    error
	networks   []docker.Network
	containers map[string]docker.Container
}

func (f *fakeDocker) Events(ctx context.Context) (<-chan docker.Event, <-chan error) {
	return nil, nil
}

func (f *fakeDocker) ListContainers(ctx context.Context) ([]docker.Container, error) {
	out := make([]docker.Container, 0, len(f.containers))
	for _, c := range f.containers {
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeDocker) InspectContainer(ctx context.Context, id string) (docker.Container, error) {
	if id == f.self.ID || id == homeNetwork {
		if f.selfErr != nil {
			return docker.Container{}, f.selfErr
		}
		return f.self, nil
	}
	c, ok := f.containers[id]
	if !ok {
		return docker.Container{}, errors.New("not found")
	}
	return c, nil
}

func (f *fakeDocker) ListNetworks(ctx context.Context) ([]docker.Network, error) {
	return append([]docker.Network(nil), f.networks...), nil
}

func (f *fakeDocker) InspectNetwork(ctx context.Context, id string) (docker.Network, error) {
	for _, n := range f.networks {
		if n.ID == id || n.Name == id {
			return n, nil
		}
	}
	return docker.Network{}, errors.New("not found")
}

type fakeConnector struct {
	connects []string
}

func (f *fakeConnector) Connect(ctx context.Context, networkID, containerID string) error {
	f.connects = append(f.connects, networkID)
	return nil
}

func (f *fakeConnector) Disconnect(ctx context.Context, networkID, containerID string) error {
	return nil
}

type fakeJoinRunner struct{}

func (*fakeJoinRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	if strings.Contains(line, "addr") {
		return "2: eth0    inet 172.19.0.2/16 brd 172.19.255.255 scope global eth0\n", nil
	}
	return "", nil
}

type fakeNATRunner struct {
	listOut string
	calls   []string
}

func (f *fakeNATRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, line)
	if strings.Contains(line, "-L PREROUTING") {
		return f.listOut, nil
	}
	return "", nil
}

func (f *fakeNATRunner) has(want string) bool {
	for _, c := range f.calls {
		if c == want {
			return true
		}
	}
	return false
}
