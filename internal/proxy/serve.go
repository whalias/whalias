// Package proxy terminates HTTPS for names whose target is not already
// speaking TLS. It listens for TCP only and does not intercept UDP.
package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/whalias/whalias/internal/ca"
)

// readOriginalDst reads the pre-redirect destination of a TCP connection.
// Tests replace it. Production uses Linux SO_ORIGINAL_DST.
var (
	dstMu           sync.Mutex
	readOriginalDst = readOriginalDestination
)

func setOriginalDst(fn func(net.Conn) (net.IP, error)) {
	dstMu.Lock()
	readOriginalDst = fn
	dstMu.Unlock()
}

func originalDestination(conn net.Conn) (net.IP, error) {
	dstMu.Lock()
	fn := readOriginalDst
	dstMu.Unlock()
	return fn(conn)
}

// Serve accepts TCP on listen until ctx is cancelled. The platform passes
// 10.254.254.2:8443, because iptables REDIRECT rewrites the destination to the
// wg0 address. Each accepted connection's original destination, from the
// platform's REDIRECT of TCP 443, is passed to lookup. When lookup is not ok,
// the connection is closed. Otherwise Serve presents a certificate from
// authority for that name and reverse-proxies plain HTTP to bridge:httpPort.
// A cancelled context stops the listener and returns nil.
func Serve(ctx context.Context, listen string, authority *ca.CA, lookup func(dst net.IP) (name, bridge string, httpPort int, ok bool)) error {
	if authority == nil {
		return errors.New("proxy: nil CA")
	}
	if lookup == nil {
		return errors.New("proxy: nil lookup")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	serveCtx, stop := context.WithCancel(ctx)
	defer stop()
	transport := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	defer transport.CloseIdleConnections()
	go func() {
		<-serveCtx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if serveCtx.Err() != nil {
				return nil
			}
			return err
		}
		go handle(serveCtx, conn, authority, lookup, transport)
	}
}

func handle(ctx context.Context, conn net.Conn, authority *ca.CA, lookup func(net.IP) (string, string, int, bool), transport *http.Transport) {
	dst, err := originalDestination(conn)
	if err != nil || dst == nil || dst.IsUnspecified() {
		_ = conn.Close()
		return
	}
	serveConn(ctx, conn, authority, lookup, transport, dst)
}

func serveConn(ctx context.Context, conn net.Conn, authority *ca.CA, lookup func(net.IP) (string, string, int, bool), transport *http.Transport, dst net.IP) {
	defer conn.Close()
	name, bridge, port, ok := lookup(dst)
	if !ok || !validBridgeIP(bridge) || port < 1 || port > 65535 {
		return
	}
	certPEM, keyPEM, err := authority.Sign([]string{name})
	if err != nil {
		return
	}
	leaf, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return
	}

	ln := newOneShotListener(conn)
	defer ln.Close()
	target := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(bridge, strconv.Itoa(port)),
	}
	rp := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}
	srvCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	srv := &http.Server{
		Handler: rp,
		BaseContext: func(net.Listener) context.Context {
			return srvCtx
		},
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{leaf},
			MinVersion:   tls.VersionTLS12,
		},
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	stop, cancelStop := context.WithCancel(context.Background())
	defer cancelStop()
	go func() {
		select {
		case <-ctx.Done():
			_ = srv.Close()
		case <-stop.Done():
		}
	}()
	_ = srv.ServeTLS(ln, "", "")
}

type oneShotListener struct {
	ch   chan net.Conn
	addr net.Addr
	once sync.Once
}

func newOneShotListener(conn net.Conn) *oneShotListener {
	l := &oneShotListener{
		ch:   make(chan net.Conn, 1),
		addr: conn.LocalAddr(),
	}
	l.ch <- conn
	return l
}

func (l *oneShotListener) Accept() (net.Conn, error) {
	conn, ok := <-l.ch
	if !ok {
		return nil, net.ErrClosed
	}
	return &notifyCloseConn{Conn: conn, after: l.Close}, nil
}

func (l *oneShotListener) Close() error {
	l.once.Do(func() { close(l.ch) })
	return nil
}

func (l *oneShotListener) Addr() net.Addr { return l.addr }

type notifyCloseConn struct {
	net.Conn
	once  sync.Once
	after func() error
}

func (c *notifyCloseConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { _ = c.after() })
	return err
}
