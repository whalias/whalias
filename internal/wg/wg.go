// Package wg brings up the kernel WireGuard interface inside the controller.
// The private key stays in the data volume. Only the public key is written
// under the bind-mounted keys directory.
package wg

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	LinkName   = "wg0"
	Address    = "10.254.254.2/32"
	PeerCIDR   = "10.254.254.1/32"
	ListenPort = 51820
	MTU        = 1280
	KeepAlive  = 25

	privateKeyName  = "controller.key"
	publicKeyName   = "controller.pub"
	peerKeyName     = "mac.pub"
	maxKeyFileBytes = 4096
	maxKeyScanBytes = 64 * 1024
)

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

// Options selects paths for one controller.
type Options struct {
	DataDir     string
	KeysDir     string
	ProcConfDir string
	ForwardFile string
}

// Boot writes the controller public key, waits for the Mac public key, and
// configures wg0. ip_forward must be writable; a failure there stops startup.
func Boot(ctx context.Context, run Runner, opt Options) error {
	keyPath, err := EnsureKey(opt.DataDir, opt.KeysDir)
	if err != nil {
		return err
	}
	peer, err := WaitPeer(ctx, opt.KeysDir)
	if err != nil {
		return err
	}
	return Setup(ctx, run, SetupConfig{
		PrivateKeyFile: keyPath,
		PeerPublic:     peer,
		ProcConfDir:    opt.ProcConfDir,
		ForwardFile:    opt.ForwardFile,
	})
}

// EnsureKey loads or creates the controller private key in dataDir and writes
// the public key to keysDir/controller.pub. The private key is not written there.
func EnsureKey(dataDir, keysDir string) (string, error) {
	if err := CheckDirs(dataDir, keysDir); err != nil {
		return "", err
	}
	privPath := filepath.Join(dataDir, privateKeyName)
	pubPath := filepath.Join(keysDir, publicKeyName)
	privB64, pubB64, err := loadOrCreate(privPath)
	if err != nil {
		return "", err
	}
	if !validKey(privB64) || !validKey(pubB64) || privB64 == pubB64 {
		return "", errors.New("wg: generated or loaded key pair is invalid")
	}
	if err := checkKeysDir(keysDir, privB64); err != nil {
		return "", err
	}
	if err := writeFile(pubPath, []byte(pubB64+"\n"), 0o644); err != nil {
		return "", err
	}
	return privPath, nil
}

// CheckDirs creates both directories and refuses a layout where the data
// directory is the public keys directory or lives under it, through a path,
// a symlink, or a second mount of the same directory. It also refuses a keys
// directory that already holds a private key file.
func CheckDirs(dataDir, keysDir string) error {
	if dataDir == "" || keysDir == "" {
		return errors.New("wg: data and keys directories are required")
	}
	dataAbs, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	keysAbs, err := filepath.Abs(keysDir)
	if err != nil {
		return err
	}
	if pathWithin(dataAbs, keysAbs) {
		return fmt.Errorf("wg: data directory %s is inside the public keys directory %s", dataDir, keysDir)
	}
	if keysInfo, err := os.Stat(keysAbs); err == nil {
		if !keysInfo.IsDir() {
			return fmt.Errorf("wg: public keys path %s is not a directory", keysDir)
		}
		if err := checkAncestorsOutside(dataAbs, keysInfo, dataDir, keysDir); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(keysDir, 0o755); err != nil {
		return err
	}
	keysInfo, err := os.Stat(keysDir)
	if err != nil {
		return err
	}
	if !keysInfo.IsDir() {
		return fmt.Errorf("wg: public keys path %s is not a directory", keysDir)
	}
	dir, err := filepath.EvalSymlinks(dataAbs)
	if err != nil {
		return err
	}
	for {
		info, err := os.Stat(dir)
		if err != nil {
			return err
		}
		if os.SameFile(info, keysInfo) {
			return fmt.Errorf("wg: data directory %s is inside the public keys directory %s", dataDir, keysDir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return checkKeysDir(keysDir, "")
}

func pathWithin(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func checkAncestorsOutside(dataAbs string, keysInfo os.FileInfo, dataDir, keysDir string) error {
	cur := dataAbs
	for {
		if _, err := os.Lstat(cur); err == nil {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return nil
		}
		cur = parent
	}
	resolved, err := filepath.EvalSymlinks(cur)
	if err != nil {
		return err
	}
	for {
		info, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		if os.SameFile(info, keysInfo) {
			return fmt.Errorf("wg: data directory %s is inside the public keys directory %s", dataDir, keysDir)
		}
		parent := filepath.Dir(resolved)
		if parent == resolved {
			break
		}
		resolved = parent
	}
	return nil
}

func checkKeysDir(keysDir, forbiddenSecret string) error {
	return filepath.WalkDir(keysDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == keysDir {
			return nil
		}
		rel, err := filepath.Rel(keysDir, path)
		if err != nil {
			rel = d.Name()
		}
		lower := strings.ToLower(d.Name())
		if strings.HasSuffix(lower, ".key") || strings.HasSuffix(lower, ".priv") || strings.HasSuffix(lower, ".conf") {
			return fmt.Errorf("wg: public keys directory %s contains private key %s", keysDir, rel)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		f, err := openRegularFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(f, maxKeyScanBytes))
		_ = f.Close()
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(data, []byte("PrivateKey")) || bytes.Contains(data, []byte("PRIVATE KEY")) {
			return fmt.Errorf("wg: public keys directory %s contains private key material in %s", keysDir, rel)
		}
		if forbiddenSecret != "" && bytes.Contains(data, []byte(forbiddenSecret)) {
			return fmt.Errorf("wg: public keys directory %s contains controller private key in %s", keysDir, rel)
		}
		return nil
	})
}

// WaitPeer blocks until keysDir/mac.pub contains a valid WireGuard public key
// or ctx is cancelled. A partial, symlinked, or malformed file is treated as not ready.
func WaitPeer(ctx context.Context, keysDir string) (string, error) {
	path := filepath.Join(keysDir, peerKeyName)
	read := func() (string, bool) {
		f, err := openRegularFile(path)
		if err != nil {
			return "", false
		}
		b, err := readBounded(f, maxKeyFileBytes)
		_ = f.Close()
		if err != nil {
			return "", false
		}
		s := strings.TrimSpace(string(b))
		return s, validKey(s)
	}
	if s, ok := read(); ok {
		return s, nil
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-tick.C:
			if s, ok := read(); ok {
				return s, nil
			}
		}
	}
}

// SetupConfig is the kernel interface configuration.
type SetupConfig struct {
	PrivateKeyFile string
	PeerPublic     string
	ProcConfDir    string
	ForwardFile    string
}

// Setup creates wg0 if needed and installs forwarding, rp_filter, and MASQUERADE.
// Docker bridge subnets are not routed. The peer route is only 10.254.254.1/32.
func Setup(ctx context.Context, run Runner, cfg SetupConfig) error {
	if strings.TrimSpace(cfg.PrivateKeyFile) == "" {
		return errors.New("wg: private key file is required")
	}
	if !validKey(strings.TrimSpace(cfg.PeerPublic)) {
		return errors.New("wg: mac public key is not a WireGuard key")
	}
	if _, err := run.Run(ctx, "ip", "link", "show", LinkName); err != nil {
		if _, err := run.Run(ctx, "ip", "link", "add", LinkName, "type", "wireguard"); err != nil {
			return fmt.Errorf("add %s: %w", LinkName, err)
		}
	}
	if _, err := run.Run(ctx, "wg", "set", LinkName,
		"private-key", cfg.PrivateKeyFile,
		"listen-port", strconv.Itoa(ListenPort),
		"peer", strings.TrimSpace(cfg.PeerPublic),
		"allowed-ips", PeerCIDR,
		"persistent-keepalive", strconv.Itoa(KeepAlive),
	); err != nil {
		return fmt.Errorf("wg set: %w", err)
	}
	if _, err := run.Run(ctx, "ip", "addr", "replace", Address, "dev", LinkName); err != nil {
		return fmt.Errorf("address %s: %w", LinkName, err)
	}
	if _, err := run.Run(ctx, "ip", "link", "set", LinkName, "mtu", strconv.Itoa(MTU), "up"); err != nil {
		return fmt.Errorf("mtu %s: %w", LinkName, err)
	}
	if _, err := run.Run(ctx, "ip", "route", "replace", PeerCIDR, "dev", LinkName); err != nil {
		return fmt.Errorf("peer route: %w", err)
	}
	if err := EnableForward(cfg.ForwardFile); err != nil {
		return err
	}
	if err := ApplyRPFilter(cfg.ProcConfDir); err != nil {
		return err
	}
	if err := EnsureMasquerade(ctx, run); err != nil {
		return err
	}
	return nil
}

// EnableForward sets net.ipv4.ip_forward to 1. A failed write is fatal.
func EnableForward(path string) error {
	if err := os.WriteFile(path, []byte("1\n"), 0o644); err != nil {
		return fmt.Errorf("set ip_forward: %w", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read ip_forward: %w", err)
	}
	if strings.TrimSpace(string(b)) != "1" {
		return fmt.Errorf("ip_forward is %q", strings.TrimSpace(string(b)))
	}
	return nil
}

// ApplyRPFilter sets rp_filter to 0 for every interface directory present now,
// including all and default.
func ApplyRPFilter(confDir string) error {
	entries, err := os.ReadDir(confDir)
	if err != nil {
		return fmt.Errorf("read rp_filter dir: %w", err)
	}
	var first error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(confDir, e.Name(), "rp_filter")
		if err := os.WriteFile(path, []byte("0\n"), 0o644); err != nil && first == nil {
			first = fmt.Errorf("rp_filter %s: %w", e.Name(), err)
		}
	}
	return first
}

// WatchRPFilter calls apply when a new interface directory appears.
// primed, when non-nil, is closed after the first successful scan.
func WatchRPFilter(ctx context.Context, confDir string, every time.Duration, apply func() error, primed chan<- struct{}) {
	if every <= 0 {
		every = time.Second
	}
	seen := map[string]struct{}{}
	ready := false
	scan := func() {
		names, err := listIfaces(confDir)
		if err != nil {
			return
		}
		cur := make(map[string]struct{}, len(names))
		fresh := false
		for _, name := range names {
			cur[name] = struct{}{}
			if _, ok := seen[name]; !ok && ready {
				fresh = true
			}
		}
		if !ready {
			seen = cur
			ready = true
			if primed != nil {
				close(primed)
				primed = nil
			}
			return
		}
		if fresh && apply != nil {
			if err := apply(); err != nil {
				return
			}
		}
		seen = cur
	}
	scan()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			scan()
		}
	}
}

// EnsureMasquerade accepts forwarded traffic and masquerades every output
// interface except wg0. Existing rules are left in place. Nothing is flushed.
func EnsureMasquerade(ctx context.Context, run Runner) error {
	if _, err := run.Run(ctx, "iptables", "-P", "FORWARD", "ACCEPT"); err != nil {
		return err
	}
	if _, err := run.Run(ctx, "iptables", "-C", "FORWARD", "-j", "ACCEPT"); err != nil {
		if _, err := run.Run(ctx, "iptables", "-A", "FORWARD", "-j", "ACCEPT"); err != nil {
			return err
		}
	}
	_, retErr := run.Run(ctx, "iptables", "-t", "nat", "-C", "POSTROUTING", "-o", "wg0", "-j", "RETURN")
	_, masqErr := run.Run(ctx, "iptables", "-t", "nat", "-C", "POSTROUTING", "-j", "MASQUERADE")
	switch {
	case retErr == nil && masqErr == nil:
		return nil
	case retErr != nil && masqErr != nil:
		if _, err := run.Run(ctx, "iptables", "-t", "nat", "-A", "POSTROUTING", "-o", "wg0", "-j", "RETURN"); err != nil {
			return err
		}
		if _, err := run.Run(ctx, "iptables", "-t", "nat", "-A", "POSTROUTING", "-j", "MASQUERADE"); err != nil {
			return err
		}
	case retErr != nil:
		if _, err := run.Run(ctx, "iptables", "-t", "nat", "-I", "POSTROUTING", "1", "-o", "wg0", "-j", "RETURN"); err != nil {
			return err
		}
	default:
		if _, err := run.Run(ctx, "iptables", "-t", "nat", "-A", "POSTROUTING", "-j", "MASQUERADE"); err != nil {
			return err
		}
	}
	return nil
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

func listIfaces(confDir string) ([]string, error) {
	entries, err := os.ReadDir(confDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func loadOrCreate(privPath string) (privB64, pubB64 string, err error) {
	if _, err := os.Lstat(privPath); err == nil {
		f, err := openRegularFile(privPath)
		if err != nil {
			return "", "", fmt.Errorf("existing private key: %w", err)
		}
		defer f.Close()
		b, err := readBounded(f, maxKeyFileBytes)
		if err != nil {
			return "", "", fmt.Errorf("existing private key: %w", err)
		}
		privB64 = strings.TrimSpace(string(b))
		if !validKey(privB64) {
			return "", "", errors.New("existing private key: invalid WireGuard key")
		}
		pubB64, err = publicFromPrivate(privB64)
		if err != nil {
			return "", "", fmt.Errorf("existing private key: %w", err)
		}
		if err := f.Chmod(0o600); err != nil {
			return "", "", fmt.Errorf("existing private key: %w", err)
		}
		return privB64, pubB64, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	privB64, pubB64, err = generate()
	if err != nil {
		return "", "", err
	}
	if err := writeFile(privPath, []byte(privB64+"\n"), 0o600); err != nil {
		return "", "", err
	}
	return privB64, pubB64, nil
}

func openRegularFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("wg: %s is not a regular file", filepath.Base(path))
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	finfo, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !finfo.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("wg: %s is not a regular file", filepath.Base(path))
	}
	return f, nil
}

func readBounded(f *os.File, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("wg: file exceeds %d bytes", limit)
	}
	return data, nil
}

func generate() (string, string, error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return encodeKey(key.Bytes()), encodeKey(key.PublicKey().Bytes()), nil
}

func publicFromPrivate(privB64 string) (string, error) {
	raw, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(privB64))
	if err != nil {
		return "", err
	}
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", err
	}
	return encodeKey(key.PublicKey().Bytes()), nil
}

func encodeKey(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

func validKey(s string) bool {
	if len(s) != 44 {
		return false
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil || len(raw) != 32 || encodeKey(raw) != s {
		return false
	}
	for _, b := range raw {
		if b != 0 {
			return true
		}
	}
	return false
}

// writeFile writes through a fresh, exclusively created temp file so a
// planted symlink or a stale temp file with wider permissions is never followed.
func writeFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
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
	if err := f.Chmod(mode); err != nil {
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
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}
