package wg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeyStaysOutOfPublicDirAndReloads(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	keys := filepath.Join(root, "keys")
	priv, err := EnsureKey(data, keys)
	if err != nil {
		t.Fatal(err)
	}
	if priv != filepath.Join(data, privateKeyName) {
		t.Fatalf("private path %s", priv)
	}
	if _, err := os.Stat(filepath.Join(keys, privateKeyName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private key written into the public directory")
	}
	info, err := os.Stat(priv)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private mode %o", info.Mode().Perm())
	}
	pubInfo, err := os.Stat(filepath.Join(keys, publicKeyName))
	if err != nil {
		t.Fatal(err)
	}
	if pubInfo.Mode().Perm() != 0o644 {
		t.Fatalf("public mode %o", pubInfo.Mode().Perm())
	}
	first, err := os.ReadFile(filepath.Join(keys, publicKeyName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureKey(data, keys); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(keys, publicKeyName))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || strings.TrimSpace(string(first)) == "" {
		t.Fatalf("public key changed or empty: %q %q", first, second)
	}
	entries, err := os.ReadDir(keys)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != publicKeyName {
		t.Fatalf("keys dir %#v", entries)
	}
}

// macPub is a syntactically valid WireGuard public key for tests.
const macPub = "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="

func TestWaitPeer(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WaitPeer(ctx, dir); err == nil {
		t.Fatal("waited after cancel")
	}
	if err := os.WriteFile(filepath.Join(dir, peerKeyName), []byte(macPub+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := WaitPeer(context.Background(), dir)
	if err != nil || got != macPub {
		t.Fatalf("peer %q %v", got, err)
	}
}

func TestWaitPeerIgnoresPartialKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, peerKeyName), []byte(macPub[:20]), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if got, err := WaitPeer(ctx, dir); err == nil {
		t.Fatalf("accepted a partial key %q", got)
	}
}

func TestDataInsideKeysIsRefused(t *testing.T) {
	root := t.TempDir()
	keys := filepath.Join(root, "keys")
	if err := os.MkdirAll(keys, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{keys, filepath.Join(keys, "data"), filepath.Join(keys, "a", "b")} {
		if _, err := EnsureKey(data, keys); err == nil {
			t.Fatalf("accepted data %s under keys %s", data, keys)
		}
		if _, err := os.Stat(filepath.Join(data, privateKeyName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("private key written under %s", data)
		}
	}
	// Refusal must happen before MkdirAll mutates keys.
	entries, err := os.ReadDir(keys)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("CheckDirs mutated keys directory before refusing: %#v", entries)
	}
}

func TestDataSymlinkIntoKeysIsRefused(t *testing.T) {
	root := t.TempDir()
	keys := filepath.Join(root, "keys")
	if err := os.MkdirAll(filepath.Join(keys, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{keys, filepath.Join(keys, "inner")} {
		link := filepath.Join(root, "data-"+filepath.Base(target))
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := EnsureKey(link, keys); err == nil {
			t.Fatalf("accepted data symlink to %s", target)
		}
		if _, err := EnsureKey(filepath.Join(link, "nested"), keys); err == nil {
			t.Fatalf("accepted nested data under symlink to %s", target)
		}
		if _, err := os.Stat(filepath.Join(target, "nested")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("created nested dir inside %s through symlink", target)
		}
	}
	if _, err := os.Stat(filepath.Join(keys, privateKeyName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private key written into the public directory")
	}
}

func TestKeysDirWithPrivateKeyIsRefused(t *testing.T) {
	cases := []struct {
		name    string
		relPath string
		content string
	}{
		{"top-level key", "mac.key", "secret\n"},
		{"uppercase key", "MAC.KEY", "secret\n"},
		{"nested key", filepath.Join("sub", "mac.key"), "secret\n"},
		{"wg conf", "wg0.conf", "[Interface]\n"},
		{"priv suffix", "host.priv", "secret\n"},
		{"wg config text under pub name", "peer.pub", "PrivateKey = abc\n"},
		{"pem private key under pub name", "ca.pub", "-----BEGIN PRIVATE KEY-----\nabc\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			keys := filepath.Join(root, "keys")
			full := filepath.Join(keys, tc.relPath)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := EnsureKey(filepath.Join(root, "data"), keys); err == nil {
				t.Fatalf("%s: started with private key material in keys dir", tc.name)
			}
			if _, err := os.Stat(filepath.Join(keys, publicKeyName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s: wrote controller.pub after refusing", tc.name)
			}
		})
	}
}

func TestControllerPrivateKeyCopiedIntoKeysDirIsRefused(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	keys := filepath.Join(root, "keys")
	priv, err := EnsureKey(data, keys)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := os.ReadFile(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keys, "leaked.pub"), secret, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureKey(data, keys); err == nil {
		t.Fatal("accepted keys dir containing a copy of the controller private key")
	}
}

func TestPrivateKeySymlinkIsRefused(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	keys := filepath.Join(root, "keys")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "outside")
	if err := os.WriteFile(target, []byte(macPub+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(data, privateKeyName)); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureKey(data, keys); err == nil {
		t.Fatal("accepted a symlinked controller.key")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("chmod followed symlink to outside file: mode %o", info.Mode().Perm())
	}
}

func TestWaitPeerRejectsSymlinkAndMalformedKeys(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "real.pub")
	if err := os.WriteFile(target, []byte(macPub+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, peerKeyName)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if got, err := WaitPeer(ctx, dir); err == nil {
		t.Fatalf("followed symlinked mac.pub: %q", got)
	}
	_ = os.Remove(filepath.Join(dir, peerKeyName))

	badKeys := []string{
		macPub[:20] + "\n" + macPub[20:],               // embedded newline
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB=", // non-zero padding bits
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", // all-zero key
	}
	for _, bad := range badKeys {
		if err := os.WriteFile(filepath.Join(dir, peerKeyName), []byte(bad+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ctx2, cancel2 := context.WithTimeout(context.Background(), 250*time.Millisecond)
		got, err := WaitPeer(ctx2, dir)
		cancel2()
		if err == nil {
			t.Fatalf("accepted bad peer key %q as %q", bad, got)
		}
	}
}

func TestPlantedTempSymlinkIsNotFollowed(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	keys := filepath.Join(root, "keys")
	priv, err := EnsureKey(data, keys)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(priv, filepath.Join(keys, publicKeyName+".tmp")); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureKey(data, keys); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(priv)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("public key write went through a planted symlink into the private key")
	}
	info, err := os.Stat(priv)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private mode %o", info.Mode().Perm())
	}
}

func TestExistingKeyModeIsTightened(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	keys := filepath.Join(root, "keys")
	priv, err := EnsureKey(data, keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(priv, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureKey(data, keys); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(priv)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private mode %o", info.Mode().Perm())
	}
}

func TestSetupRejectsBadPeerKey(t *testing.T) {
	for _, peer := range []string{
		"not-a-key",
		macPub[:20] + "\n" + macPub[20:],
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB=",
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	} {
		run := &callRunner{}
		err := Setup(context.Background(), run, SetupConfig{
			PrivateKeyFile: "/data/controller.key",
			PeerPublic:     peer,
			ProcConfDir:    t.TempDir(),
			ForwardFile:    filepath.Join(t.TempDir(), "ip_forward"),
		})
		if err == nil || len(run.calls) != 0 {
			t.Fatalf("peer %q: err %v calls %#v", peer, err, run.calls)
		}
	}
	run := &callRunner{}
	err := Setup(context.Background(), run, SetupConfig{
		PrivateKeyFile: "",
		PeerPublic:     macPub,
		ProcConfDir:    t.TempDir(),
		ForwardFile:    filepath.Join(t.TempDir(), "ip_forward"),
	})
	if err == nil || len(run.calls) != 0 {
		t.Fatalf("empty private key: err %v calls %#v", err, run.calls)
	}
}

func TestSetupKernelCommands(t *testing.T) {
	root := t.TempDir()
	conf := filepath.Join(root, "conf")
	for _, name := range []string{"all", "default", "eth0"} {
		if err := os.MkdirAll(filepath.Join(conf, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	forward := filepath.Join(root, "ip_forward")
	run := &callRunner{fail: map[string]error{
		"ip link show wg0":                                errors.New("not found"),
		"iptables -C FORWARD -j ACCEPT":                   errors.New("missing"),
		"iptables -t nat -C POSTROUTING -o wg0 -j RETURN": errors.New("missing"),
		"iptables -t nat -C POSTROUTING -j MASQUERADE":    errors.New("missing"),
	}}
	err := Setup(context.Background(), run, SetupConfig{
		PrivateKeyFile: "/data/controller.key",
		PeerPublic:     macPub,
		ProcConfDir:    conf,
		ForwardFile:    forward,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"ip", "link", "show", "wg0"},
		{"ip", "link", "add", "wg0", "type", "wireguard"},
		{"wg", "set", "wg0", "private-key", "/data/controller.key", "listen-port", "51820", "peer", macPub, "allowed-ips", "10.254.254.1/32", "persistent-keepalive", "25"},
		{"ip", "addr", "replace", "10.254.254.2/32", "dev", "wg0"},
		{"ip", "link", "set", "wg0", "mtu", "1280", "up"},
		{"ip", "route", "replace", "10.254.254.1/32", "dev", "wg0"},
		{"iptables", "-P", "FORWARD", "ACCEPT"},
		{"iptables", "-C", "FORWARD", "-j", "ACCEPT"},
		{"iptables", "-A", "FORWARD", "-j", "ACCEPT"},
		{"iptables", "-t", "nat", "-C", "POSTROUTING", "-o", "wg0", "-j", "RETURN"},
		{"iptables", "-t", "nat", "-C", "POSTROUTING", "-j", "MASQUERADE"},
		{"iptables", "-t", "nat", "-A", "POSTROUTING", "-o", "wg0", "-j", "RETURN"},
		{"iptables", "-t", "nat", "-A", "POSTROUTING", "-j", "MASQUERADE"},
	}
	if len(run.calls) != len(want) {
		t.Fatalf("calls %#v", run.calls)
	}
	for i := range want {
		if !same(run.calls[i], want[i]) {
			t.Fatalf("call %d\n got %#v\nwant %#v", i, run.calls[i], want[i])
		}
	}
	for _, call := range run.calls {
		for _, arg := range call {
			if arg == "wireguard-go" || arg == "genkey" || strings.Contains(arg, "10.254.253") || strings.HasPrefix(arg, "172.") {
				t.Fatalf("unexpected arg %q in %#v", arg, call)
			}
			if arg == "-F" {
				t.Fatal("flushed a chain")
			}
		}
	}
	body, err := os.ReadFile(forward)
	if err != nil || strings.TrimSpace(string(body)) != "1" {
		t.Fatalf("ip_forward %q %v", body, err)
	}
	for _, name := range []string{"all", "default", "eth0"} {
		b, err := os.ReadFile(filepath.Join(conf, name, "rp_filter"))
		if err != nil || strings.TrimSpace(string(b)) != "0" {
			t.Fatalf("rp_filter %s %q %v", name, b, err)
		}
	}
}

func TestSetupExistingLinkSkipsAdd(t *testing.T) {
	root := t.TempDir()
	conf := filepath.Join(root, "conf", "all")
	if err := os.MkdirAll(conf, 0o755); err != nil {
		t.Fatal(err)
	}
	run := &callRunner{}
	err := Setup(context.Background(), run, SetupConfig{
		PrivateKeyFile: "/data/controller.key",
		PeerPublic:     macPub,
		ProcConfDir:    filepath.Dir(conf),
		ForwardFile:    filepath.Join(root, "ip_forward"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range run.calls {
		if len(call) >= 3 && call[0] == "ip" && call[1] == "link" && call[2] == "add" {
			t.Fatal("added wg0 that already existed")
		}
	}
}

func TestForwardFailure(t *testing.T) {
	err := EnableForward(filepath.Join(t.TempDir(), "missing", "ip_forward"))
	if err == nil {
		t.Fatal("expected ip_forward to fail")
	}
}

func TestMasqueradeIdempotent(t *testing.T) {
	run := &callRunner{}
	if err := EnsureMasquerade(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	for _, call := range run.calls {
		if len(call) >= 5 && call[1] == "-t" && (call[4] == "-A" || call[4] == "-I") {
			t.Fatalf("appended an existing nat rule: %#v", call)
		}
	}
}

func TestMasqueradeInsertsReturnBeforeExisting(t *testing.T) {
	run := &callRunner{fail: map[string]error{
		"iptables -t nat -C POSTROUTING -o wg0 -j RETURN": errors.New("missing"),
	}}
	if err := EnsureMasquerade(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if !run.has([]string{"iptables", "-t", "nat", "-I", "POSTROUTING", "1", "-o", "wg0", "-j", "RETURN"}) {
		t.Fatalf("calls %#v", run.calls)
	}
	if run.has([]string{"iptables", "-t", "nat", "-A", "POSTROUTING", "-j", "MASQUERADE"}) {
		t.Fatal("duplicated MASQUERADE")
	}
}

func TestWatchNewInterface(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "eth0"), 0o755); err != nil {
		t.Fatal(err)
	}
	var applied atomic.Int32
	primed := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go WatchRPFilter(ctx, dir, 10*time.Millisecond, func() error {
		applied.Add(1)
		return nil
	}, primed)
	select {
	case <-primed:
	case <-time.After(time.Second):
		t.Fatal("watch did not scan")
	}
	if applied.Load() != 0 {
		t.Fatal("rp_filter ran before a new interface appeared")
	}
	if err := os.Mkdir(filepath.Join(dir, "wg0"), 0o755); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for applied.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if applied.Load() == 0 {
		t.Fatal("new interface did not reapply rp_filter")
	}
	// Removing an interface and recreating it with the same name must reapply rp_filter.
	if err := os.Remove(filepath.Join(dir, "wg0")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	before := applied.Load()
	if err := os.Mkdir(filepath.Join(dir, "wg0"), 0o755); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for applied.Load() <= before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if applied.Load() <= before {
		t.Fatal("recreated interface name did not reapply rp_filter")
	}
}

type callRunner struct {
	calls [][]string
	fail  map[string]error
}

func (c *callRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := append([]string{name}, args...)
	c.calls = append(c.calls, cmd)
	if err := c.fail[strings.Join(cmd, " ")]; err != nil {
		return "", err
	}
	return "", nil
}

func (c *callRunner) has(want []string) bool {
	for _, call := range c.calls {
		if same(call, want) {
			return true
		}
	}
	return false
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
