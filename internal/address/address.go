// Package address allocates one stable tunnel address per container identity
// and publishes only that address to DNS.
package address

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// Subnet is the only prefix the Mac routes into the tunnel.
	Subnet = "10.254.253.0/24"
	// Domain is the DNS zone for container names.
	Domain = "wha.internal"

	labelProject = "com.docker.compose.project"
	labelService = "com.docker.compose.service"
	labelNumber  = "com.docker.compose.container-number"
)

// Identity is the compose project, service, and container number when those
// labels exist. Otherwise it is the container name. The same identity keeps
// its address across recreate.
func Identity(containerName string, labels map[string]string) string {
	project := labels[labelProject]
	service := labels[labelService]
	number := labels[labelNumber]
	if project != "" && service != "" && number != "" {
		return "compose:" + project + "/" + service + "/" + number
	}
	return "name:" + strings.TrimPrefix(containerName, "/")
}

// DNSNames returns the names published for a container. A container named web
// is web.wha.internal. Underscores become hyphens. Compose labels also add
// service.project.wha.internal. Reserved names are omitted.
func DNSNames(containerName string, labels map[string]string) []string {
	var names []string
	if host := sanitizeHost(strings.TrimPrefix(containerName, "/")); host != "" {
		names = appendName(names, host+"."+Domain)
	}
	project := sanitizeHost(labels[labelProject])
	service := sanitizeHost(labels[labelService])
	if project != "" && service != "" {
		names = appendName(names, service+"."+project+"."+Domain)
	}
	return names
}

// Preferred is the name the proxy certificate should use. The compose name
// wins when both exist.
func Preferred(names []string) string {
	best := ""
	bestDots := -1
	for _, name := range names {
		dots := strings.Count(name, ".")
		if dots >= bestDots {
			best = name
			bestDots = dots
		}
	}
	return best
}

func appendName(names []string, fqdn string) []string {
	fqdn = normalize(fqdn)
	if fqdn == "" || reserved(fqdn) || contains(names, fqdn) {
		return names
	}
	return append(names, fqdn)
}

func reserved(fqdn string) bool {
	return fqdn == "wha.internal" || fqdn == "whalias.wha.internal"
}

func contains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func normalize(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// sanitizeHost turns a container or label into lowercase DNS labels.
// Dots stay as separators. Anything else that is not a letter, digit, or
// hyphen becomes a hyphen.
func sanitizeHost(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var labels []string
	for _, part := range strings.Split(raw, ".") {
		label := cleanLabel(part)
		if label != "" {
			labels = append(labels, label)
		}
	}
	return strings.Join(labels, ".")
}

func cleanLabel(s string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !ok {
			r = '-'
		}
		if r == '-' {
			if b.Len() == 0 || prevHyphen {
				continue
			}
			prevHyphen = true
		} else {
			prevHyphen = false
		}
		b.WriteRune(r)
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = strings.TrimRight(out[:63], "-")
	}
	return out
}

// Entry is a snapshot of one allocated identity.
type Entry struct {
	Identity string
	IP       string
	Bridge   string
}

type allocation struct {
	Identity string   `json:"identity"`
	IP       string   `json:"ip"`
	Names    []string `json:"names"`
	Primary  string   `json:"primary,omitempty"`
	Bridge   string   `json:"bridge,omitempty"`
	HTTPPort int      `json:"httpPort,omitempty"`
}

type fileState struct {
	Allocations []allocation `json:"allocations"`
}

// Book is the sticky address table. DNS records survive container destroy so
// a recreate is not cached as NXDOMAIN. Records never include the bridge address.
type Book struct {
	mu    sync.RWMutex
	path  string
	items map[string]*allocation
	byIP  map[string]string
}

// Open reads the table at path. A missing file is an empty table. A corrupt
// file is an error, so a restart cannot forget addresses and hand them out again.
func Open(path string) (*Book, error) {
	b := &Book{
		path:  path,
		items: map[string]*allocation{},
		byIP:  map[string]string{},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return b, nil
		}
		return nil, fmt.Errorf("read addresses: %w", err)
	}
	var state fileState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse addresses: %w", err)
	}
	for i := range state.Allocations {
		item := state.Allocations[i]
		ip := net.ParseIP(item.IP).To4()
		if item.Identity == "" || !stableHost(ip) {
			return nil, fmt.Errorf("addresses: invalid entry %q ip %q", item.Identity, item.IP)
		}
		if _, dup := b.items[item.Identity]; dup {
			return nil, fmt.Errorf("addresses: duplicate identity %q", item.Identity)
		}
		if other, dup := b.byIP[ip.String()]; dup {
			return nil, fmt.Errorf("addresses: %s assigned to %q and %q", ip, other, item.Identity)
		}
		if item.Bridge != "" {
			if !validBridge(item.Bridge) {
				item.Bridge, item.HTTPPort = "", 0
			} else {
				item.Bridge = net.ParseIP(item.Bridge).To4().String()
			}
		}
		if item.Bridge == "" || item.HTTPPort < 1 || item.HTTPPort > 65535 {
			item.HTTPPort = 0
		}
		item.IP = ip.String()
		item.Names = normalizeAll(item.Names)
		copied := item
		b.items[item.Identity] = &copied
		b.byIP[item.IP] = item.Identity
	}
	return b, nil
}

// Upsert allocates a stable address for identity if needed, publishes names to
// that address, and persists the table before it returns. The bridge address
// is not part of the DNS record.
func (b *Book) Upsert(identity string, names []string) (net.IP, error) {
	if identity == "" {
		return nil, errors.New("address: empty identity")
	}
	names = normalizeAll(names)
	b.mu.Lock()
	defer b.mu.Unlock()

	item := b.items[identity]
	created := false
	if item == nil {
		ip, err := b.allocateLocked()
		if err != nil {
			return nil, err
		}
		item = &allocation{Identity: identity, IP: ip.String()}
		b.items[identity] = item
		b.byIP[item.IP] = identity
		created = true
	}
	prevNames := append([]string(nil), item.Names...)
	prevPrimary := item.Primary
	item.Names = append([]string(nil), names...)
	if len(names) > 0 {
		item.Primary = Preferred(names)
	}
	if err := b.saveLocked(); err != nil {
		if created {
			delete(b.items, identity)
			delete(b.byIP, item.IP)
		} else {
			item.Names = prevNames
			item.Primary = prevPrimary
		}
		return nil, err
	}
	return net.ParseIP(item.IP).To4(), nil
}

// SetRuntime records the current bridge address and HTTP port used by the
// proxy. It does not change the stable address or the DNS record. An empty
// bridge clears the runtime target. A bridge inside the stable subnet, the
// tunnel, or loopback is refused.
func (b *Book) SetRuntime(identity, primary, bridge string, httpPort int) error {
	if bridge != "" {
		if !validBridge(bridge) {
			return fmt.Errorf("address: %q is not a bridge address", bridge)
		}
		bridge = net.ParseIP(bridge).To4().String()
	}
	if bridge == "" || httpPort < 1 || httpPort > 65535 {
		httpPort = 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	item := b.items[identity]
	if item == nil {
		return fmt.Errorf("address: unknown identity %s", identity)
	}
	prevPrimary, prevBridge, prevPort := item.Primary, item.Bridge, item.HTTPPort
	if primary != "" {
		item.Primary = normalize(primary)
	}
	item.Bridge = bridge
	item.HTTPPort = httpPort
	if err := b.saveLocked(); err != nil {
		item.Primary, item.Bridge, item.HTTPPort = prevPrimary, prevBridge, prevPort
		return err
	}
	return nil
}

// Records is the DNS callback. It returns stable addresses only.
func (b *Book) Records(name string) ([]net.IP, bool) {
	key := normalize(name)
	b.mu.RLock()
	defer b.mu.RUnlock()
	var ips []net.IP
	seen := map[string]struct{}{}
	for _, item := range b.items {
		if !contains(item.Names, key) {
			continue
		}
		if _, ok := seen[item.IP]; ok {
			continue
		}
		seen[item.IP] = struct{}{}
		ip := net.ParseIP(item.IP).To4()
		if !stableHost(ip) || ip.String() == item.Bridge {
			continue
		}
		ips = append(ips, append(net.IP(nil), ip...))
	}
	if len(ips) == 0 {
		return nil, false
	}
	sortIPs(ips)
	return ips, true
}

// Entries returns a snapshot of all allocated identities.
func (b *Book) Entries() []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Entry, 0, len(b.items))
	for _, item := range b.items {
		out = append(out, Entry{
			Identity: item.Identity,
			IP:       item.IP,
			Bridge:   item.Bridge,
		})
	}
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && out[j-1].Identity > out[j].Identity {
			out[j-1], out[j] = out[j], out[j-1]
			j--
		}
	}
	return out
}

// Lookup is the proxy callback. name is the certificate name, bridge is the
// current container address on a project network, and httpPort is the port
// Probe selected.
func (b *Book) Lookup(dst net.IP) (name, bridge string, httpPort int, ok bool) {
	ip := dst.To4()
	if ip == nil {
		return "", "", 0, false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	id := b.byIP[ip.String()]
	item := b.items[id]
	if item == nil || item.Primary == "" || !validBridge(item.Bridge) || item.HTTPPort < 1 || item.HTTPPort > 65535 {
		return "", "", 0, false
	}
	return item.Primary, item.Bridge, item.HTTPPort, true
}

var (
	stableNet = mustCIDR(Subnet)
	tunnelNet = mustCIDR("10.254.254.0/30")
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func stableHost(ip net.IP) bool {
	ip = ip.To4()
	return ip != nil && stableNet.Contains(ip) && ip[3] != 0 && ip[3] != 255
}

func validBridge(s string) bool {
	ip := net.ParseIP(s).To4()
	return ip != nil && !stableNet.Contains(ip) && !tunnelNet.Contains(ip) &&
		!ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsMulticast() && !ip.Equal(net.IPv4bcast)
}

func (b *Book) allocateLocked() (net.IP, error) {
	for host := 1; host <= 254; host++ {
		ip := net.IPv4(10, 254, 253, byte(host)).To4()
		if _, used := b.byIP[ip.String()]; used {
			continue
		}
		return ip, nil
	}
	return nil, errors.New("address block 10.254.253.0/24 is full")
}

func (b *Book) saveLocked() error {
	state := fileState{Allocations: make([]allocation, 0, len(b.items))}
	for _, item := range b.items {
		state.Allocations = append(state.Allocations, *item)
	}
	sortAllocations(state.Allocations)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(b.path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(b.path), "."+filepath.Base(b.path)+".tmp-*")
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
	if err := os.Rename(tmp, b.path); err != nil {
		return err
	}
	ok = true
	return nil
}

func normalizeAll(names []string) []string {
	var out []string
	for _, name := range names {
		name = normalize(name)
		if name != "" {
			out = appendName(out, name)
		}
	}
	return out
}

func sortIPs(ips []net.IP) {
	for i := 1; i < len(ips); i++ {
		j := i
		for j > 0 && ips[j-1].String() > ips[j].String() {
			ips[j-1], ips[j] = ips[j], ips[j-1]
			j--
		}
	}
}

func sortAllocations(items []allocation) {
	for i := 1; i < len(items); i++ {
		j := i
		for j > 0 && items[j-1].Identity > items[j].Identity {
			items[j-1], items[j] = items[j], items[j-1]
			j--
		}
	}
}
