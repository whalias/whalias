package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/whalias/whalias/internal/ca"
)

func TestProbeDefaultPorts(t *testing.T) {
	if probeHTTPSPort != 443 {
		t.Fatalf("https port %d", probeHTTPSPort)
	}
	want := []int{80, 8080, 3000, 5000, 8000}
	if !reflect.DeepEqual(probeHTTPPorts, want) {
		t.Fatalf("http ports %v", probeHTTPPorts)
	}
}

func TestProbeRedirectsOnlyWhen443ClosedAndHTTPAccepts(t *testing.T) {
	// 443 and 80 cannot be bound without root. Probe's real TCP connects run
	// against free ports standing in for that decision order. TestProbeDefaultPorts
	// locks the production ports.
	https := listenLocal(t)
	first := listenLocal(t)
	second := listenLocal(t)
	labeled := listenLocal(t)
	defer https.ln.Close()
	defer first.ln.Close()
	defer second.ln.Close()
	defer labeled.ln.Close()

	oldHTTPS := probeHTTPSPort
	oldHTTP := probeHTTPPorts
	probeHTTPSPort = https.port
	probeHTTPPorts = []int{first.port, second.port, closedPort(t)}
	t.Cleanup(func() {
		probeHTTPSPort = oldHTTPS
		probeHTTPPorts = oldHTTP
	})

	ctx := context.Background()
	label := strconv.Itoa(labeled.port)

	got := mustProbe(t, ctx, label)
	if got.Redirect || got.HTTPPort != 0 {
		t.Fatalf("443 open with label: %+v", got)
	}
	got = mustProbe(t, ctx, "")
	if got.Redirect || got.HTTPPort != 0 {
		t.Fatalf("443 open: %+v", got)
	}

	https.ln.Close()
	got = mustProbe(t, ctx, "nope")
	if !got.Redirect || got.HTTPPort != first.port {
		t.Fatalf("443 closed, first HTTP open: %+v", got)
	}
	if got, err := Probe(ctx, "localhost", ""); err == nil || got.Redirect {
		t.Fatalf("host name probed as a bridge address: %+v %v", got, err)
	}
	for _, badIP := range []string{"0.0.0.0", "::", "224.0.0.1"} {
		if got, err := Probe(ctx, badIP, ""); err == nil || got.Redirect {
			t.Fatalf("%s probed as a bridge address: %+v %v", badIP, got, err)
		}
	}
	got = mustProbe(t, ctx, strconv.Itoa(https.port))
	if got.Redirect || got.HTTPPort != 0 {
		t.Fatalf("http port label equal to HTTPS port: %+v", got)
	}

	first.ln.Close()
	got = mustProbe(t, ctx, "")
	if !got.Redirect || got.HTTPPort != second.port {
		t.Fatalf("first HTTP closed, second open: %+v", got)
	}

	got = mustProbe(t, ctx, label)
	if !got.Redirect || got.HTTPPort != labeled.port {
		t.Fatalf("label port: %+v", got)
	}
	labeled.ln.Close()
	got = mustProbe(t, ctx, label)
	if got.Redirect || got.HTTPPort != 0 {
		t.Fatalf("label closed while a later fallback is open: %+v", got)
	}

	second.ln.Close()
	got = mustProbe(t, ctx, "")
	if got.Redirect || got.HTTPPort != 0 {
		t.Fatalf("nothing accepts: %+v", got)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Probe(canceled, "127.0.0.1", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled probe: %v", err)
	}
	if _, err := Probe(ctx, "  ", ""); err == nil {
		t.Fatal("empty bridge address was accepted")
	}
}

func TestServeProxiesPlainHTTP(t *testing.T) {
	var mu sync.Mutex
	var sawHost, sawProto string
	var sawTLS bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawHost = r.Host
		sawProto = r.Header.Get("X-Forwarded-Proto")
		sawTLS = r.TLS != nil
		mu.Unlock()
		_, _ = w.Write([]byte("HTTP-BACKEND"))
	}))
	defer backend.Close()
	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	bridge, portStr, err := net.SplitHostPort(backendURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	httpPort, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	authority, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stable := net.ParseIP("10.254.253.10")
	const name = "web.myproject.wha.internal"
	setOriginalDst(func(net.Conn) (net.IP, error) {
		return stable, nil
	})
	t.Cleanup(func() { setOriginalDst(readOriginalDestination) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := freeAddr(t)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, addr, authority, func(dst net.IP) (string, string, int, bool) {
			if !dst.Equal(stable) {
				return "", "", 0, false
			}
			return name, bridge, httpPort, true
		})
	}()

	conn := dialUntil(t, addr)
	pool := x509.NewCertPool()
	pool.AddCert(parseCertPEM(t, authority.CertificatePEM()))
	tlsConn := tls.Client(conn, &tls.Config{RootCAs: pool, ServerName: name})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatal(err)
	}
	peer := tlsConn.ConnectionState().PeerCertificates
	if len(peer) == 0 || len(peer[0].DNSNames) != 1 || peer[0].DNSNames[0] != name {
		t.Fatalf("peer names %#v", peer)
	}
	req, err := http.NewRequest(http.MethodGet, "https://"+name+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Close = true
	if err := req.Write(tlsConn); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	_ = tlsConn.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "HTTP-BACKEND" {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	mu.Lock()
	host, proto, usedTLS := sawHost, sawProto, sawTLS
	mu.Unlock()
	if host != name || proto != "https" || usedTLS {
		t.Fatalf("backend host %q proto %q tls %v", host, proto, usedTLS)
	}

	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("UDP listen failed; proxy should not bind UDP: %v", err)
	}
	_ = pc.Close()

	// HTTP/2 clients (browsers, curl, Go http.Client) must work over ALPN h2,
	// and an ambient HTTP_PROXY must not hijack local bridge forwarding.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	h2Client := &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
			TLSClientConfig:   &tls.Config{RootCAs: pool, ServerName: name},
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", addr)
			},
		},
	}
	defer h2Client.CloseIdleConnections()
	h2Resp, err := h2Client.Get("https://" + name + "/h2")
	if err != nil {
		t.Fatalf("HTTP/2 request failed: %v", err)
	}
	h2Body, err := io.ReadAll(h2Resp.Body)
	_ = h2Resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if h2Resp.StatusCode != http.StatusOK || h2Resp.ProtoMajor != 2 || string(h2Body) != "HTTP-BACKEND" {
		t.Fatalf("HTTP/2: status %d proto %s body %q", h2Resp.StatusCode, h2Resp.Proto, h2Body)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

func TestServeClosesWhenLookupIsNotOK(t *testing.T) {
	setOriginalDst(func(net.Conn) (net.IP, error) {
		return net.ParseIP("10.254.253.99"), nil
	})
	t.Cleanup(func() { setOriginalDst(readOriginalDestination) })

	authority, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := freeAddr(t)
	go func() {
		_ = Serve(ctx, addr, authority, func(net.IP) (string, string, int, bool) {
			return "", "", 0, false
		})
	}()
	assertClosed(t, dialUntil(t, addr))
}

func TestServeClosesWhenLookupIsUnsafe(t *testing.T) {
	setOriginalDst(func(net.Conn) (net.IP, error) {
		return net.ParseIP("10.254.253.10"), nil
	})
	t.Cleanup(func() { setOriginalDst(readOriginalDestination) })

	authority, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backend := listenLocal(t)
	defer backend.ln.Close()
	for _, tc := range []struct {
		label  string
		name   string
		bridge string
		port   int
	}{
		{"name outside zone", "evil.example.com", "127.0.0.1", backend.port},
		{"empty name", "", "127.0.0.1", backend.port},
		{"bridge host name", "web.wha.internal", "localhost", backend.port},
		{"unspecified ipv4 bridge", "web.wha.internal", "0.0.0.0", backend.port},
		{"unspecified ipv6 bridge", "web.wha.internal", "::", backend.port},
		{"multicast bridge", "web.wha.internal", "224.0.0.1", backend.port},
		{"zero port", "web.wha.internal", "127.0.0.1", 0},
		{"port too high", "web.wha.internal", "127.0.0.1", 65536},
	} {
		t.Run(tc.label, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			addr := freeAddr(t)
			go func() {
				_ = Serve(ctx, addr, authority, func(net.IP) (string, string, int, bool) {
					return tc.name, tc.bridge, tc.port, true
				})
			}()
			assertClosed(t, dialUntil(t, addr))
		})
	}
}

func TestServeClosesWithoutOriginalDestination(t *testing.T) {
	setOriginalDst(readOriginalDestination)
	t.Cleanup(func() { setOriginalDst(readOriginalDestination) })

	authority, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := freeAddr(t)
	go func() {
		_ = Serve(ctx, addr, authority, func(net.IP) (string, string, int, bool) {
			t.Error("lookup called without an original destination")
			return "web.wha.internal", "127.0.0.1", 80, true
		})
	}()
	assertClosed(t, dialUntil(t, addr))
}

func mustProbe(t *testing.T, ctx context.Context, label string) Decision {
	t.Helper()
	got, err := Probe(ctx, "127.0.0.1", label)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

type localListener struct {
	ln   net.Listener
	port int
}

func listenLocal(t *testing.T) *localListener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return &localListener{ln: ln, port: ln.Addr().(*net.TCPAddr).Port}
}

func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func dialUntil(t *testing.T, addr string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			return conn
		}
		last = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("dial %s: %v", addr, last)
	return nil
}

func assertClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 8)
	n, err := conn.Read(buf)
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("closed connection: n=%d err=%v data=%q", n, err, buf[:n])
	}
}

func parseCertPEM(t *testing.T, certPEM []byte) *x509.Certificate {
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
