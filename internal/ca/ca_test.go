package ca

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNameConstraintRejectsOutside(t *testing.T) {
	c := loadTemp(t)
	root := parsePEM(t, c.CertificatePEM())
	if !root.PermittedDNSDomainsCritical {
		t.Fatal("name constraint is not critical")
	}
	if len(root.PermittedDNSDomains) != 1 || root.PermittedDNSDomains[0] != "wha.internal" {
		t.Fatalf("permitted DNS names %#v", root.PermittedDNSDomains)
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)

	for _, name := range []string{"wha.internal", "web.myproject.wha.internal", "a.b.wha.internal"} {
		leaf := signLeaf(t, c, name)
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: name}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Sign refuses these names, so the leaves are forged with the root key to
	// prove the verifier also rejects them.
	for _, name := range []string{"evil.example.com", "notwha.internal", "wha.internal.evil.example.com", "*.com"} {
		leaf := forgeLeaf(t, c, &x509.Certificate{DNSNames: []string{name}})
		assertConstraintRejects(t, name, leaf, x509.VerifyOptions{Roots: roots, DNSName: name})
	}
	for _, ip := range []string{"10.254.253.10", "127.0.0.1", "::1"} {
		leaf := forgeLeaf(t, c, &x509.Certificate{IPAddresses: []net.IP{net.ParseIP(ip)}})
		assertConstraintRejects(t, ip, leaf, x509.VerifyOptions{Roots: roots, DNSName: ip})
	}
	leaf := forgeLeaf(t, c, &x509.Certificate{
		DNSNames:       []string{"web.wha.internal"},
		EmailAddresses: []string{"someone@example.com"},
	})
	assertConstraintRejects(t, "email", leaf, x509.VerifyOptions{Roots: roots})
	evilURI, _ := url.Parse("https://evil.example.com/")
	leaf = forgeLeaf(t, c, &x509.Certificate{
		DNSNames: []string{"web.wha.internal"},
		URIs:     []*url.URL{evilURI},
	})
	assertConstraintRejects(t, "uri", leaf, x509.VerifyOptions{Roots: roots})

	if len(root.ExtKeyUsage) != 1 || root.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatalf("root extended key usage %#v", root.ExtKeyUsage)
	}
	clientLeaf := signLeaf(t, c, "web.wha.internal")
	if _, err := clientLeaf.Verify(x509.VerifyOptions{
		Roots:     roots,
		DNSName:   "web.wha.internal",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err == nil {
		t.Fatal("root accepted non-server-auth verification")
	}

	// Forging an intermediate CA with the root key must be rejected by MaxPathLenZero.
	interKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	interTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(99),
		Subject:               pkix.Name{CommonName: "Forged Intermediate"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	interDER, err := x509.CreateCertificate(rand.Reader, interTmpl, c.cert, &interKey.PublicKey, c.key)
	if err != nil {
		t.Fatal(err)
	}
	interCert, err := x509.ParseCertificate(interDER)
	if err != nil {
		t.Fatal(err)
	}
	subLeafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	subLeafTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(100),
		DNSNames:              []string{"web.wha.internal"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	subLeafDER, err := x509.CreateCertificate(rand.Reader, subLeafTmpl, interCert, &subLeafKey.PublicKey, interKey)
	if err != nil {
		t.Fatal(err)
	}
	subLeaf, err := x509.ParseCertificate(subLeafDER)
	if err != nil {
		t.Fatal(err)
	}
	inters := x509.NewCertPool()
	inters.AddCert(interCert)
	if _, err := subLeaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: inters,
		DNSName:       "web.wha.internal",
	}); err == nil {
		t.Fatal("root allowed an intermediate CA")
	}
}

func TestSignRefusesNamesOutsideZone(t *testing.T) {
	c := loadTemp(t)
	for _, name := range []string{
		"evil.example.com",
		"notwha.internal",
		"wha.internal.evil.example.com",
		"10.254.253.10",
		"*.wha.internal",
		"web_app.wha.internal",
		"evil.com\x00.wha.internal",
		"-web.wha.internal",
		"a..wha.internal",
		"\u212A.wha.internal",
	} {
		if _, _, err := c.Sign([]string{name}); err == nil {
			t.Fatalf("Sign accepted %q", name)
		}
		if _, _, err := c.Sign([]string{"web.wha.internal", name}); err == nil {
			t.Fatalf("Sign accepted %q beside an in-zone name", name)
		}
	}
	var nilCA *CA
	if _, _, err := nilCA.Sign([]string{"web.wha.internal"}); err == nil {
		t.Fatal("nil CA signed a certificate")
	}
	if _, _, err := (&CA{}).Sign([]string{"web.wha.internal"}); err == nil {
		t.Fatal("uninitialized CA signed a certificate")
	}
}

func TestLoadRejectsWiderStoredRoot(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:                big.NewInt(7),
		Subject:                     pkix.Name{CommonName: "Whalias"},
		NotBefore:                   time.Now().Add(-time.Hour),
		NotAfter:                    time.Now().Add(time.Hour),
		IsCA:                        true,
		BasicConstraintsValid:       true,
		KeyUsage:                    x509.KeyUsageCertSign,
		MaxPathLenZero:              true,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{"wha.internal"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	writeFile(t, filepath.Join(dir, "ca.crt"), certPEM, 0o644)
	writeFile(t, filepath.Join(dir, "ca.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600)

	if _, err := LoadOrCreate(dir); err == nil {
		t.Fatal("loaded a root without IP, email, and URI constraints")
	}
	got, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, certPEM) {
		t.Fatal("a rejected root was replaced")
	}
}

func TestLoadRejectsExtraPEMBlocksAndNonSelfSignedRoot(t *testing.T) {
	dir := t.TempDir()
	c := mustLoad(t, dir)
	origCert := c.CertificatePEM()
	origKey, err := os.ReadFile(filepath.Join(dir, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}

	// Extra unconstrained certificate appended to ca.crt must be rejected, not served.
	rogueKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rogueTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(13),
		Subject:               pkix.Name{CommonName: "Rogue Root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	rogueDER, err := x509.CreateCertificate(rand.Reader, rogueTmpl, rogueTmpl, &rogueKey.PublicKey, rogueKey)
	if err != nil {
		t.Fatal(err)
	}
	roguePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rogueDER})

	withExtraCert := t.TempDir()
	writeFile(t, filepath.Join(withExtraCert, "ca.crt"), append(append([]byte(nil), origCert...), roguePEM...), 0o644)
	writeFile(t, filepath.Join(withExtraCert, "ca.key"), origKey, 0o600)
	if _, err := LoadOrCreate(withExtraCert); err == nil {
		t.Fatal("loaded ca.crt with an appended second certificate")
	}

	// Private key appended to ca.crt must be rejected.
	withExtraKey := t.TempDir()
	writeFile(t, filepath.Join(withExtraKey, "ca.crt"), append(append([]byte(nil), origCert...), origKey...), 0o644)
	writeFile(t, filepath.Join(withExtraKey, "ca.key"), origKey, 0o600)
	if _, err := LoadOrCreate(withExtraKey); err == nil {
		t.Fatal("loaded ca.crt with an appended private key")
	}

	// Extra PEM block appended to ca.key must be rejected.
	withTrailingKey := t.TempDir()
	writeFile(t, filepath.Join(withTrailingKey, "ca.crt"), origCert, 0o644)
	writeFile(t, filepath.Join(withTrailingKey, "ca.key"), append(append([]byte(nil), origKey...), origKey...), 0o600)
	if _, err := LoadOrCreate(withTrailingKey); err == nil {
		t.Fatal("loaded ca.key with trailing PEM block")
	}

	// Non-self-signed certificate (signed by rogueKey, matching constraints) must be rejected.
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	notSelfTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(14),
		Subject:               pkix.Name{CommonName: "Whalias"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		MaxPathLenZero:        true,
	}
	rootConstraints(notSelfTmpl)
	notSelfDER, err := x509.CreateCertificate(rand.Reader, notSelfTmpl, rogueTmpl, &otherKey.PublicKey, rogueKey)
	if err != nil {
		t.Fatal(err)
	}
	otherKeyDER, err := x509.MarshalPKCS8PrivateKey(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	notSelfDir := t.TempDir()
	writeFile(t, filepath.Join(notSelfDir, "ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: notSelfDER}), 0o644)
	writeFile(t, filepath.Join(notSelfDir, "ca.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: otherKeyDER}), 0o600)
	if _, err := LoadOrCreate(notSelfDir); err == nil {
		t.Fatal("loaded a non-self-signed certificate")
	}
}

func TestKeyNeverLeavesDirectoryThroughSymlinks(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	planted := filepath.Join(outside, "stolen")
	if err := os.Symlink(planted, filepath.Join(dir, "ca.key.tmp")); err != nil {
		t.Fatal(err)
	}
	c := mustLoad(t, dir)
	if _, err := os.Lstat(planted); !os.IsNotExist(err) {
		t.Fatalf("key was written through a planted symlink: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(dir, "ca.key")); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("ca.key is not a 0600 regular file: %v %v", info, err)
	}

	linked := t.TempDir()
	writeFile(t, filepath.Join(linked, "ca.crt"), c.CertificatePEM(), 0o644)
	if err := os.Symlink(filepath.Join(dir, "ca.key"), filepath.Join(linked, "ca.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(linked); err == nil {
		t.Fatal("loaded a key through a symlink")
	}

	dangling := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "absent.key"), filepath.Join(dangling, "ca.key")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dangling, "ca.crt"), c.CertificatePEM(), 0o644)
	if _, err := LoadOrCreate(dangling); err == nil {
		t.Fatal("loaded with a dangling key symlink")
	}
	if _, err := os.Lstat(filepath.Join(outside, "absent.key")); !os.IsNotExist(err) {
		t.Fatal("a key was created outside the directory")
	}
}

func forgeLeaf(t *testing.T, c *CA, tmpl *x509.Certificate) *x509.Certificate {
	t.Helper()
	certPEM, _, err := c.issue(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	return parsePEM(t, certPEM)
}

func assertConstraintRejects(t *testing.T, label string, leaf *x509.Certificate, opts x509.VerifyOptions) {
	t.Helper()
	_, err := leaf.Verify(opts)
	if err == nil {
		t.Fatalf("name constraint accepted %s", label)
	}
	var inv x509.CertificateInvalidError
	if !errors.As(err, &inv) || inv.Reason != x509.CANotAuthorizedForThisName {
		t.Fatalf("%s: expected name constraint rejection, got %v", label, err)
	}
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func TestSignUsesExactNames(t *testing.T) {
	c := loadTemp(t)
	certPEM, keyPEM, err := c.Sign([]string{
		" Web.MyProject.wha.internal.",
		"wha.internal",
		"Web.MyProject.wha.internal.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	leaf := parsePEM(t, certPEM)
	want := []string{"Web.MyProject.wha.internal", "wha.internal"}
	if len(leaf.DNSNames) != len(want) {
		t.Fatalf("DNSNames %#v", leaf.DNSNames)
	}
	for i := range want {
		if leaf.DNSNames[i] != want[i] {
			t.Fatalf("DNSNames %#v", leaf.DNSNames)
		}
	}
	life := leaf.NotAfter.Sub(leaf.NotBefore)
	if life < 29*24*time.Hour || life > 31*24*time.Hour {
		t.Fatalf("leaf lifetime %s", life)
	}
	if _, _, err := c.Sign(nil); err == nil {
		t.Fatal("Sign(nil) succeeded")
	}
	if _, _, err := c.Sign([]string{"  ."}); err == nil {
		t.Fatal("Sign of an empty name succeeded")
	}
}

func TestLoadOrCreateKeepsKeyInDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data", "ca")
	c := mustLoad(t, dir)
	pemBytes := c.CertificatePEM()
	if bytes.Contains(pemBytes, []byte("PRIVATE KEY")) {
		t.Fatal("CertificatePEM contains a private key")
	}

	certPEM, leafKeyPEM, err := c.Sign([]string{"web.wha.internal"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(certPEM, pemBytes) {
		t.Fatal("leaf PEM is the root PEM")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, entry := range entries {
		got[entry.Name()] = true
	}
	if len(got) != 2 || !got["ca.crt"] || !got["ca.key"] {
		t.Fatalf("directory contents %#v", got)
	}
	info, err := os.Stat(filepath.Join(dir, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %o", info.Mode().Perm())
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(leafKeyPEM, keyPEM) {
		t.Fatal("Sign returned the CA private key")
	}
	if bytes.Contains(pemBytes, keyPEM) {
		t.Fatal("CertificatePEM contains the key file")
	}

	again, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again.CertificatePEM(), pemBytes) {
		t.Fatal("reload changed the certificate")
	}
}

func loadTemp(t *testing.T) *CA {
	t.Helper()
	return mustLoad(t, t.TempDir())
}

func mustLoad(t *testing.T, dir string) *CA {
	t.Helper()
	c, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func signLeaf(t *testing.T, c *CA, name string) *x509.Certificate {
	t.Helper()
	certPEM, _, err := c.Sign([]string{name})
	if err != nil {
		t.Fatal(err)
	}
	return parsePEM(t, certPEM)
}

func parsePEM(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
