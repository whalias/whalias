// Package ca is the name-constrained local root for wha.internal.
// The private key stays in the directory passed to LoadOrCreate.
// This package does not install the certificate into any trust store.
package ca

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	certFileName = "ca.crt"
	keyFileName  = "ca.key"
	zone         = "wha.internal"

	leafLife   = 30 * 24 * time.Hour
	rootLife   = 10 * 365 * 24 * time.Hour
	maxPEMSize = 1 << 20
)

var (
	oidExtBasicConstraints = asn1.ObjectIdentifier{2, 5, 29, 19}
	oidExtNameConstraints  = asn1.ObjectIdentifier{2, 5, 29, 30}
)

// CA is the local root. Its certificate is name-constrained to wha.internal.
type CA struct {
	cert    *x509.Certificate
	key     crypto.Signer
	certPEM []byte
}

// LoadOrCreate loads the root from dir, or creates it there the first time.
// The certificate is written to ca.crt and the private key to ca.key with
// mode 0600. A stored certificate that is not a critical name-constrained
// CA for wha.internal is rejected rather than replaced.
func LoadOrCreate(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ca: create directory: %w", err)
	}
	certPath := filepath.Join(dir, certFileName)
	keyPath := filepath.Join(dir, keyFileName)
	_, certErr := os.Lstat(certPath)
	_, keyErr := os.Lstat(keyPath)
	switch {
	case certErr == nil && keyErr == nil:
		return load(certPath, keyPath)
	case os.IsNotExist(certErr) && os.IsNotExist(keyErr):
		return create(certPath, keyPath)
	case certErr != nil && !os.IsNotExist(certErr):
		return nil, fmt.Errorf("ca: stat certificate: %w", certErr)
	case keyErr != nil && !os.IsNotExist(keyErr):
		return nil, fmt.Errorf("ca: stat key: %w", keyErr)
	default:
		return nil, errors.New("ca: certificate and key must both exist or both be absent")
	}
}

// CertificatePEM returns the root certificate in PEM form.
// The private key is not included.
func (c *CA) CertificatePEM() []byte {
	if c == nil || len(c.certPEM) == 0 {
		return nil
	}
	out := make([]byte, len(c.certPEM))
	copy(out, c.certPEM)
	return out
}

// Sign issues a leaf certificate for the exact names it is given, in that
// order. Surrounding spaces and a single trailing dot are removed. Empty
// names are an error. The returned key is a new leaf key, not the CA key.
//
// Sign refuses a name outside wha.internal, and any name that is not
// letters, digits, and hyphens in dot-separated labels. Verifiers would also
// reject a name outside wha.internal because of the root's name constraint.
func (c *CA) Sign(names []string) (certPEM, keyPEM []byte, err error) {
	if c == nil || c.cert == nil || c.key == nil {
		return nil, nil, errors.New("ca: uninitialized CA")
	}
	dnsNames, err := exactNames(names)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{DNSNames: dnsNames}
	if len(dnsNames[0]) <= 64 {
		tmpl.Subject = pkix.Name{CommonName: dnsNames[0]}
	}
	return c.issue(tmpl)
}

// issue signs a server leaf for the subject and names already set on tmpl.
// Sign is the only caller outside tests and checks those names first.
func (c *CA) issue(tmpl *x509.Certificate) (certPEM, keyPEM []byte, err error) {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("ca: generate leaf key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl.SerialNumber = serial
	tmpl.NotBefore = now.Add(-time.Minute)
	tmpl.NotAfter = now.Add(leafLife)
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	tmpl.BasicConstraintsValid = true
	tmpl.IsCA = false
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &leafKey.PublicKey, c.key)
	if err != nil {
		return nil, nil, fmt.Errorf("ca: sign leaf: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return nil, nil, fmt.Errorf("ca: marshal leaf key: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func exactNames(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, errors.New("ca: no names to sign")
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		name = strings.TrimSuffix(name, ".")
		if name == "" {
			return nil, errors.New("ca: empty name")
		}
		if !inZone(name) {
			return nil, fmt.Errorf("ca: %q is not a host name in %s", name, zone)
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, errors.New("ca: no names to sign")
	}
	return out, nil
}

func inZone(name string) bool {
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			ch := label[i]
			if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' {
				return false
			}
		}
	}
	lower := strings.ToLower(name)
	return lower == zone || strings.HasSuffix(lower, "."+zone)
}

// rootConstraints limits every name form the root could vouch for. DNS,
// email, and URI names must be in wha.internal. Every IPv4 and IPv6 address
// is excluded, because an absent IP constraint would permit all of them.
func rootConstraints(tmpl *x509.Certificate) {
	tmpl.PermittedDNSDomainsCritical = true
	tmpl.PermittedDNSDomains = []string{zone}
	tmpl.PermittedEmailAddresses = []string{zone}
	tmpl.PermittedURIDomains = []string{zone}
	tmpl.ExcludedIPRanges = []*net.IPNet{
		{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, 32)},
		{IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)},
	}
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
}

func create(certPath, keyPath string) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ca: generate root key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Whalias",
			Organization: []string{"Whalias"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(rootLife),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		MaxPathLenZero:        true,
	}
	rootConstraints(tmpl)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("ca: create root: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("ca: marshal root key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := writeFileAtomic(certPath, certPEM, 0o644); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(keyPath, keyPEM, 0o600); err != nil {
		_ = os.Remove(certPath)
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("ca: parse root: %w", err)
	}
	return &CA{cert: cert, key: key, certPEM: certPEM}, nil
}

func load(certPath, keyPath string) (*CA, error) {
	certFile, err := openRegularFile(certPath)
	if err != nil {
		return nil, err
	}
	defer certFile.Close()
	keyFile, err := openRegularFile(keyPath)
	if err != nil {
		return nil, err
	}
	defer keyFile.Close()

	certPEM, err := readBounded(certFile, certFileName)
	if err != nil {
		return nil, err
	}
	keyPEM, err := readBounded(keyFile, keyFileName)
	if err != nil {
		return nil, err
	}
	trimmedCert := bytes.TrimSpace(certPEM)
	if !bytes.HasPrefix(trimmedCert, []byte("-----BEGIN CERTIFICATE-----")) {
		return nil, errors.New("ca: certificate PEM has unexpected prefix")
	}
	block, rest := pem.Decode(trimmedCert)
	if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("ca: certificate PEM must contain a single certificate block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca: parse certificate: %w", err)
	}
	if err := checkRoot(cert); err != nil {
		return nil, err
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return nil, err
	}
	if !publicKeyEqual(cert.PublicKey, key.Public()) {
		return nil, errors.New("ca: stored key does not match the certificate")
	}
	if err := keyFile.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("ca: restrict key: %w", err)
	}
	canonicalCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	return &CA{cert: cert, key: key, certPEM: canonicalCertPEM}, nil
}

func checkRoot(cert *x509.Certificate) error {
	now := time.Now()
	if !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return errors.New("ca: stored certificate is not a certificate authority")
	}
	if cert.KeyUsage&^(x509.KeyUsageCertSign|x509.KeyUsageCRLSign) != 0 {
		return errors.New("ca: stored certificate has unexpected key usages")
	}
	if len(cert.UnhandledCriticalExtensions) != 0 {
		return errors.New("ca: stored certificate has unhandled critical extensions")
	}
	if !hasCriticalExtension(cert, oidExtBasicConstraints) || !hasCriticalExtension(cert, oidExtNameConstraints) {
		return errors.New("ca: stored certificate constraints are not critical")
	}
	if !bytes.Equal(cert.RawIssuer, cert.RawSubject) {
		return errors.New("ca: stored certificate is not self-issued")
	}
	if err := cert.CheckSignatureFrom(cert); err != nil {
		return fmt.Errorf("ca: stored certificate self-signature invalid: %w", err)
	}
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return errors.New("ca: stored certificate is outside its validity period")
	}
	if !cert.MaxPathLenZero || cert.MaxPathLen != 0 {
		return errors.New("ca: stored certificate allows intermediate CAs")
	}
	want := &x509.Certificate{}
	rootConstraints(want)
	if !cert.PermittedDNSDomainsCritical ||
		!equalStrings(cert.PermittedDNSDomains, want.PermittedDNSDomains) ||
		!equalStrings(cert.PermittedEmailAddresses, want.PermittedEmailAddresses) ||
		!equalStrings(cert.PermittedURIDomains, want.PermittedURIDomains) ||
		len(cert.PermittedIPRanges) != 0 ||
		!equalNets(cert.ExcludedIPRanges, want.ExcludedIPRanges) {
		return errors.New("ca: stored certificate is not a wha.internal name-constrained CA")
	}
	if len(cert.ExcludedDNSDomains) != 0 || len(cert.ExcludedEmailAddresses) != 0 || len(cert.ExcludedURIDomains) != 0 {
		return errors.New("ca: stored certificate has unexpected exclusions")
	}
	if len(cert.UnknownExtKeyUsage) != 0 || len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		return errors.New("ca: stored certificate is not limited to server authentication")
	}
	return nil
}

func hasCriticalExtension(cert *x509.Certificate, oid asn1.ObjectIdentifier) bool {
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oid) {
			return ext.Critical
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalNets(a, b []*net.IPNet) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].String() != b[i].String() {
			return false
		}
	}
	return true
}

// openRegularFile refuses symlinks and other non-regular files both before
// open (avoiding blocking on FIFOs) and at open time via O_NOFOLLOW, so the
// key is never read from or chmodded outside the CA directory.
func openRegularFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("ca: %s is not a regular file", filepath.Base(path))
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("ca: open %s: %w", filepath.Base(path), err)
	}
	finfo, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("ca: stat %s: %w", filepath.Base(path), err)
	}
	if !finfo.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("ca: %s is not a regular file", filepath.Base(path))
	}
	return f, nil
}

func readBounded(f *os.File, name string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(f, maxPEMSize+1))
	if err != nil {
		return nil, fmt.Errorf("ca: read %s: %w", name, err)
	}
	if len(data) > maxPEMSize {
		return nil, fmt.Errorf("ca: %s is too large", name)
	}
	return data, nil
}

func parseKey(pemBytes []byte) (crypto.Signer, error) {
	trimmed := bytes.TrimSpace(pemBytes)
	if !bytes.HasPrefix(trimmed, []byte("-----BEGIN ")) {
		return nil, errors.New("ca: key PEM has unexpected prefix")
	}
	block, rest := pem.Decode(trimmed)
	if block == nil || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("ca: key PEM must contain a single key block")
	}
	var (
		key any
		err error
	)
	switch block.Type {
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("ca: unsupported key type %s", block.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("ca: parse key: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, errors.New("ca: private key cannot sign")
	}
	return signer, nil
}

func publicKeyEqual(a, b crypto.PublicKey) bool {
	type equaler interface {
		Equal(crypto.PublicKey) bool
	}
	eq, ok := a.(equaler)
	return ok && eq.Equal(b)
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("ca: serial: %w", err)
	}
	if n.Sign() == 0 {
		return big.NewInt(1), nil
	}
	return n, nil
}

// writeFileAtomic creates a new temporary file in path's directory with
// O_EXCL and mode 0600, so a planted file or symlink cannot receive the data
// or widen its mode, then renames it over path.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("ca: create %s: %w", filepath.Base(path), err)
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("ca: write %s: %w", filepath.Base(path), err)
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("ca: chmod %s: %w", filepath.Base(path), err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("ca: sync %s: %w", filepath.Base(path), err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("ca: close %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("ca: rename %s: %w", filepath.Base(path), err)
	}
	return nil
}
