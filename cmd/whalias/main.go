// Command whalias is the privileged controller. It terminates kernel WireGuard,
// joins project networks, publishes stable addresses, and translates them.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/whalias/whalias/internal/address"
	"github.com/whalias/whalias/internal/ca"
	"github.com/whalias/whalias/internal/dns"
	"github.com/whalias/whalias/internal/docker"
	"github.com/whalias/whalias/internal/join"
	"github.com/whalias/whalias/internal/nat"
	"github.com/whalias/whalias/internal/proxy"
	"github.com/whalias/whalias/internal/wg"
)

const (
	homeNetwork = "whalias"
	dnsAddr     = ":5387"
	// REDIRECT on wg0 rewrites the destination to wg0's own address, so the
	// proxy must bind exactly that address, and only after wg0 has it.
	proxyAddr      = "10.254.254.2:8443"
	procConf       = "/proc/sys/net/ipv4/conf"
	procForward    = "/proc/sys/net/ipv4/ip_forward"
	reconcileEvery = 15 * time.Second
)

func main() {
	log.SetFlags(log.LstdFlags)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	dataDir := env("WHALIAS_DATA", "/data")
	keysDir := env("WHALIAS_KEYS", "/keys")
	if err := wg.CheckDirs(dataDir, keysDir); err != nil {
		return err
	}
	book, err := address.Open(dataDir + "/addresses.json")
	if err != nil {
		return err
	}
	authority, err := ca.LoadOrCreate(dataDir)
	if err != nil {
		return err
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- dns.Serve(ctx, dnsAddr, book.Records)
	}()

	bootErr := make(chan error, 1)
	go func() {
		bootErr <- wg.Boot(ctx, wg.OSRunner{}, wg.Options{
			DataDir:     dataDir,
			KeysDir:     keysDir,
			ProcConfDir: procConf,
			ForwardFile: procForward,
		})
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		if err == nil {
			return errors.New("dns or proxy exited")
		}
		return err
	case err := <-bootErr:
		if err != nil {
			return err
		}
	}
	go func() {
		errCh <- proxy.Serve(ctx, proxyAddr, authority, book.Lookup)
	}()

	go wg.WatchRPFilter(ctx, procConf, time.Second, func() error {
		if err := wg.ApplyRPFilter(procConf); err != nil {
			log.Printf("rp_filter: %v", err)
		}
		return nil
	}, nil)

	cli, err := docker.New(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	self, err := cli.InspectContainer(ctx, homeNetwork)
	if err != nil {
		return err
	}
	home, err := cli.InspectNetwork(ctx, homeNetwork)
	if err != nil {
		return err
	}
	if err := join.CheckHome(toJoin(home)); err != nil {
		return err
	}
	homeEP, ok := self.Networks[homeNetwork]
	if !ok || homeEP.IP == "" {
		return fmt.Errorf("controller %s is not attached to network %s", self.Name, homeNetwork)
	}
	mgr, err := join.New(self.ID, dataDir+"/left-networks.json", netAdapter{cli}, join.OSRunner{})
	if err != nil {
		return err
	}
	mgr.OnJoined(func() {
		if err := wg.ApplyRPFilter(procConf); err != nil {
			log.Printf("rp_filter: %v", err)
		}
	})
	gw := home.Gateway
	if gw == "" {
		gw = homeEP.Gateway
	}
	if err := mgr.CaptureHome(ctx, gw, homeEP.IP); err != nil {
		return err
	}

	ctrl := &controller{book: book, cli: cli, natRun: wg.OSRunner{}, join: mgr, selfID: self.ID, probe: proxy.Probe}
	return ctrl.watch(ctx, errCh)
}

type dockerAPI interface {
	Events(ctx context.Context) (<-chan docker.Event, <-chan error)
	ListContainers(ctx context.Context) ([]docker.Container, error)
	InspectContainer(ctx context.Context, id string) (docker.Container, error)
	ListNetworks(ctx context.Context) ([]docker.Network, error)
	InspectNetwork(ctx context.Context, id string) (docker.Network, error)
}

type controller struct {
	book       *address.Book
	cli        dockerAPI
	natRun     nat.Runner
	join       *join.Manager
	selfID     string
	probe      func(ctx context.Context, bridgeIP, httpPortLabel string) (proxy.Decision, error)
	prunedOnce bool
}

type netAdapter struct{ c *docker.Client }

func (a netAdapter) Connect(ctx context.Context, networkID, containerID string) error {
	return a.c.ConnectNetwork(ctx, networkID, containerID)
}

func (a netAdapter) Disconnect(ctx context.Context, networkID, containerID string) error {
	return a.c.DisconnectNetwork(ctx, networkID, containerID)
}

func (c *controller) watch(ctx context.Context, errCh <-chan error) error {
	c.reconcile(ctx)
	delay := time.Second
	tick := time.NewTicker(reconcileEvery)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		started := time.Now()
		err := c.oneWatch(ctx, errCh, tick)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && !errors.Is(err, errRestart) {
			return err
		}
		if time.Since(started) > 30*time.Second {
			delay = time.Second
		}
		log.Printf("docker events: reconnecting")
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case serveErr := <-errCh:
			timer.Stop()
			if serveErr == nil {
				return errors.New("dns or proxy exited")
			}
			return serveErr
		case <-timer.C:
		}
		if delay < 30*time.Second {
			delay *= 2
		}
		c.reconcile(ctx)
	}
}

var errRestart = errors.New("event stream ended")

func (c *controller) oneWatch(ctx context.Context, errCh <-chan error, tick *time.Ticker) error {
	sub, cancel := context.WithCancel(ctx)
	defer cancel()
	events, errs := c.cli.Events(sub)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			if err == nil {
				return errors.New("dns or proxy exited")
			}
			return err
		case <-tick.C:
			c.reconcile(ctx)
		case err, ok := <-errs:
			if !ok || err == nil {
				return errRestart
			}
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("docker events: %v", err)
			return errRestart
		case ev, ok := <-events:
			if !ok {
				return errRestart
			}
			c.handle(ctx, ev)
		}
	}
}

func (c *controller) reconcile(ctx context.Context) {
	nets, err := c.cli.ListNetworks(ctx)
	if err != nil {
		log.Printf("list networks: %v", err)
		return
	}
	// Without the current attachments the overlap check is incomplete, so no
	// network is joined and container NAT is left untouched on this pass.
	self, err := c.cli.InspectContainer(ctx, c.selfID)
	if err != nil {
		log.Printf("inspect self: %v", err)
		return
	}
	attached := map[string]bool{}
	for _, ep := range self.Networks {
		if ep.NetworkID != "" {
			attached[ep.NetworkID] = true
		}
	}
	joined := make([]join.Network, 0, len(nets))
	for _, n := range nets {
		joined = append(joined, toJoin(n))
	}
	if err := c.join.Reconcile(ctx, joined, attached); err != nil {
		log.Printf("reconcile networks: %v", err)
	}
	c.reconcileContainers(ctx)
}

func (c *controller) reconcileContainers(ctx context.Context) {
	ctrs, err := c.cli.ListContainers(ctx)
	if err != nil {
		log.Printf("list containers: %v", err)
		return
	}
	live := make(map[string]bool, len(ctrs))
	for _, ctr := range ctrs {
		if c.skipContainer(ctr) {
			continue
		}
		live[address.Identity(ctr.Name, ctr.Labels)] = true
		if err := c.syncContainer(ctx, ctr); err != nil {
			log.Printf("sync %s: %v", ctr.Name, err)
		}
	}
	// Clear runtime proxy targets and remove PREROUTING rules for identities
	// whose containers no longer exist, while keeping their DNS records.
	for _, entry := range c.book.Entries() {
		if live[entry.Identity] {
			continue
		}
		if entry.Bridge != "" {
			if err := c.book.SetRuntime(entry.Identity, "", "", 0); err != nil {
				log.Printf("clear runtime %s: %v", entry.Identity, err)
			}
		}
		if entry.Bridge != "" || !c.prunedOnce {
			if err := nat.Remove(ctx, c.natRun, entry.IP); err != nil {
				log.Printf("remove nat %s: %v", entry.IP, err)
			}
		}
	}
	c.prunedOnce = true
}

func (c *controller) handle(ctx context.Context, ev docker.Event) {
	switch ev.Type {
	case "network":
		if err := c.join.Handle(ctx, join.Event{
			Action:     ev.Action,
			ID:         ev.ID,
			Attributes: ev.Attributes,
		}, func(ctx context.Context, id string) (join.Network, error) {
			n, err := c.cli.InspectNetwork(ctx, id)
			if err != nil {
				return join.Network{}, err
			}
			return toJoin(n), nil
		}); err != nil {
			log.Printf("network %s: %v", ev.Action, err)
		}
		switch ev.Action {
		case "connect", "disconnect":
			id := ev.Attributes["container"]
			if c.join.IsSelf(id) {
				c.reconcileContainers(ctx)
			} else if id != "" {
				c.syncID(ctx, id)
			}
		case "destroy":
			c.reconcileContainers(ctx)
		}
	case "container":
		if ev.Action == "destroy" {
			// Keep the DNS record so a recreate is not cached as NXDOMAIN, but
			// drop any stale NAT rule or proxy target if the container is gone.
			c.reconcileContainers(ctx)
			return
		}
		if !wantsSync(ev.Action) {
			return
		}
		c.syncID(ctx, ev.ID)
	}
}

func (c *controller) syncID(ctx context.Context, id string) {
	ctr, err := c.cli.InspectContainer(ctx, id)
	if err != nil {
		if docker.IsNotFound(err) {
			c.reconcileContainers(ctx)
			return
		}
		log.Printf("inspect %s: %v", id, err)
		return
	}
	if err := c.syncContainer(ctx, ctr); err != nil {
		log.Printf("sync %s: %v", ctr.Name, err)
	}
}

func (c *controller) syncContainer(ctx context.Context, ctr docker.Container) error {
	if c.skipContainer(ctr) {
		return nil
	}
	names := address.DNSNames(ctr.Name, ctr.Labels)
	if len(names) == 0 {
		return nil
	}
	id := address.Identity(ctr.Name, ctr.Labels)
	ip, err := c.book.Upsert(id, names)
	if err != nil {
		return err
	}
	bridge := bridgeIP(ctr, c.join.IsJoined)
	if bridge == "" {
		if err := c.book.SetRuntime(id, address.Preferred(names), "", 0); err != nil {
			return err
		}
		return nat.Remove(ctx, c.natRun, ip.String())
	}
	probeFn := c.probe
	if probeFn == nil {
		probeFn = proxy.Probe
	}
	decision, err := probeFn(ctx, bridge, ctr.Labels["dev.whalias.http-port"])
	if err != nil {
		log.Printf("probe %s: %v", ctr.Name, err)
		decision = proxy.Decision{}
	}
	if err := c.book.SetRuntime(id, address.Preferred(names), bridge, decision.HTTPPort); err != nil {
		return err
	}
	return nat.Install(ctx, c.natRun, ip.String(), bridge, decision.Redirect)
}

func (c *controller) skipContainer(ctr docker.Container) bool {
	if c.join != nil && c.join.IsSelf(ctr.ID) {
		return true
	}
	return skipContainer(ctr)
}

func skipContainer(ctr docker.Container) bool {
	if ctr.Name == "" || ctr.Name == homeNetwork {
		return true
	}
	switch ctr.Labels["dev.whalias.role"] {
	case "controller", "relay", "network":
		return true
	default:
		return false
	}
}

// bridgeIP picks the container's address on a project network the controller
// has joined. Addresses inside the stable subnet or the tunnel are never used.
func bridgeIP(ctr docker.Container, joined func(networkID string) bool) string {
	if ctr.State != "" && ctr.State != "running" && ctr.State != "paused" {
		return ""
	}
	var names []string
	for name, ep := range ctr.Networks {
		if ep.IP == "" || skippedBridge(name) || !joined(ep.NetworkID) || reservedIP(ep.IP) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return ctr.Networks[names[0]].IP
}

func reservedIP(s string) bool {
	ip := net.ParseIP(s).To4()
	if ip == nil {
		return true
	}
	if ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) {
		return true
	}
	for _, cidr := range []string{address.Subnet, "10.254.254.0/30", "127.0.0.0/8"} {
		if _, n, err := net.ParseCIDR(cidr); err == nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

func skippedBridge(name string) bool {
	switch name {
	case "host", "none", "bridge", homeNetwork:
		return true
	default:
		return false
	}
}

func wantsSync(action string) bool {
	switch action {
	case "create", "start", "restart", "die", "stop", "kill", "rename", "update", "pause", "unpause":
		return true
	default:
		return false
	}
}

func toJoin(n docker.Network) join.Network {
	return join.Network{
		ID:           n.ID,
		Name:         n.Name,
		Driver:       n.Driver,
		Subnet:       n.Subnet,
		Subnets:      append([]string(nil), n.Subnets...),
		Gateway:      n.Gateway,
		ContainerIDs: n.ContainerIDs,
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
