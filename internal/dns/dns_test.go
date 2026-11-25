package dns

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
)

func TestServeReturnsCallbackAddress(t *testing.T) {
	want := net.ParseIP("10.254.253.10")
	other := net.ParseIP("10.254.253.11")

	var mu sync.Mutex
	invoked := map[string]int{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr := freeAddr(t)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, addr, func(name string) ([]net.IP, bool) {
			mu.Lock()
			invoked[name]++
			mu.Unlock()
			switch name {
			case "web.wha.internal":
				return []net.IP{want, other}, true
			case "wha.internal":
				return []net.IP{net.ParseIP("10.254.253.2")}, true
			case "empty.wha.internal":
				return nil, true
			default:
				return nil, false
			}
		})
	}()

	for _, network := range []string{"udp", "tcp"} {
		t.Run(network, func(t *testing.T) {
			resp := exchange(t, network, addr, "Web.WHA.INTERNAL", mdns.TypeA)
			if resp.Rcode != mdns.RcodeSuccess {
				t.Fatalf("rcode %s", mdns.RcodeToString[resp.Rcode])
			}
			if !resp.Authoritative {
				t.Fatal("answer was not authoritative")
			}
			if len(resp.Answer) != 2 {
				t.Fatalf("answers = %d, want 2", len(resp.Answer))
			}
			first, ok := resp.Answer[0].(*mdns.A)
			if !ok || !first.A.Equal(want) || first.Hdr.Ttl != 5 {
				t.Fatalf("first answer %#v", resp.Answer[0])
			}
			second, ok := resp.Answer[1].(*mdns.A)
			if !ok || !second.A.Equal(other) || second.Hdr.Ttl != 5 {
				t.Fatalf("second answer %#v", resp.Answer[1])
			}

			mu.Lock()
			got := invoked["web.wha.internal"]
			mu.Unlock()
			if got == 0 {
				t.Fatal("callback was not called with web.wha.internal")
			}

			missing := exchange(t, network, addr, "missing.wha.internal", mdns.TypeA)
			if missing.Rcode != mdns.RcodeNameError {
				t.Fatalf("missing rcode %s", mdns.RcodeToString[missing.Rcode])
			}
			if !missing.Authoritative {
				t.Fatal("NXDOMAIN was not authoritative")
			}
			if len(missing.Ns) != 1 {
				t.Fatalf("NXDOMAIN authority = %#v", missing.Ns)
			}
			soa, ok := missing.Ns[0].(*mdns.SOA)
			if !ok || soa.Minttl != 1 || soa.Hdr.Ttl != 1 {
				t.Fatalf("SOA %#v", missing.Ns[0])
			}

			refused := exchange(t, network, addr, "example.com", mdns.TypeA)
			if refused.Rcode != mdns.RcodeRefused || refused.Authoritative {
				t.Fatalf("example.com: rcode %s aa %v", mdns.RcodeToString[refused.Rcode], refused.Authoritative)
			}
			outside := exchange(t, network, addr, "notwha.internal", mdns.TypeA)
			if outside.Rcode != mdns.RcodeRefused {
				t.Fatalf("notwha.internal rcode %s", mdns.RcodeToString[outside.Rcode])
			}

			aaaa := exchange(t, network, addr, "wha.internal", mdns.TypeAAAA)
			if aaaa.Rcode != mdns.RcodeSuccess || len(aaaa.Answer) != 0 {
				t.Fatalf("AAAA %#v rcode %s", aaaa.Answer, mdns.RcodeToString[aaaa.Rcode])
			}
		})
	}

	mu.Lock()
	if invoked["example.com"] != 0 || invoked["notwha.internal"] != 0 {
		t.Fatalf("callback saw names outside the zone: %#v", invoked)
	}
	mu.Unlock()

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

func TestServeApexAndTruncation(t *testing.T) {
	var many []net.IP
	for host := 1; host <= 60; host++ {
		many = append(many, net.IPv4(10, 254, 253, byte(host)))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := freeAddr(t)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, addr, func(name string) ([]net.IP, bool) {
			if name == "many.wha.internal" {
				return many, true
			}
			if name == "wha.internal" {
				// Even if a callback returns IPs alongside ok=false, the apex must not publish them.
				return []net.IP{net.ParseIP("10.254.253.99")}, false
			}
			return nil, false
		})
	}()

	for _, network := range []string{"udp", "tcp"} {
		apex := exchange(t, network, addr, "wha.internal", mdns.TypeA)
		if apex.Rcode != mdns.RcodeSuccess || len(apex.Answer) != 0 || len(apex.Ns) != 1 {
			t.Fatalf("%s apex A: rcode %s answer %v ns %v", network, mdns.RcodeToString[apex.Rcode], apex.Answer, apex.Ns)
		}
		soa := exchange(t, network, addr, "wha.internal", mdns.TypeSOA)
		if soa.Rcode != mdns.RcodeSuccess || len(soa.Answer) != 1 {
			t.Fatalf("%s apex SOA: rcode %s answer %v", network, mdns.RcodeToString[soa.Rcode], soa.Answer)
		}
		if _, ok := soa.Answer[0].(*mdns.SOA); !ok {
			t.Fatalf("%s apex SOA answer %#v", network, soa.Answer[0])
		}
	}

	m := new(mdns.Msg)
	m.SetQuestion("many.wha.internal.", mdns.TypeA)
	query, err := m.Pack()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(query); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	resp := new(mdns.Msg)
	if err := resp.Unpack(buf[:n]); err != nil {
		t.Fatal(err)
	}
	if !resp.Truncated || n > mdns.MinMsgSize {
		t.Fatalf("UDP without EDNS: truncated %v datagram %d bytes", resp.Truncated, n)
	}

	tcp := exchange(t, "tcp", addr, "many.wha.internal", mdns.TypeA)
	if tcp.Truncated || len(tcp.Answer) != len(many) {
		t.Fatalf("TCP: truncated %v answers %d", tcp.Truncated, len(tcp.Answer))
	}
	for i, rr := range tcp.Answer {
		a, ok := rr.(*mdns.A)
		if !ok || !a.A.Equal(many[i]) {
			t.Fatalf("TCP answer %d %#v", i, rr)
		}
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

func TestInZoneAndResponsePacketIgnored(t *testing.T) {
	for _, bad := range []string{"", ".", ".wha.internal", "a..wha.internal", "notwha.internal", "wha.internal.example"} {
		if inZone(bad) {
			t.Fatalf("inZone(%q) = true", bad)
		}
	}
	for _, good := range []string{"wha.internal", "web.wha.internal", "a.b.wha.internal"} {
		if !inZone(good) {
			t.Fatalf("inZone(%q) = false", good)
		}
	}

	var called atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := freeAddr(t)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, addr, func(name string) ([]net.IP, bool) {
			called.Add(1)
			return []net.IP{net.ParseIP("10.254.253.10")}, true
		})
	}()
	// Wait until server is ready via a normal query outside the zone.
	_ = exchange(t, "udp", addr, "example.com", mdns.TypeA)

	// Send a packet with QR=1 (Response=true) and verify no reply is sent and callback is not called.
	respPkt := new(mdns.Msg)
	respPkt.SetQuestion("web.wha.internal.", mdns.TypeA)
	respPkt.Response = true
	raw, err := respPkt.Pack()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write(raw); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	buf := make([]byte, 512)
	if n, err := conn.Read(buf); err == nil {
		t.Fatalf("server replied to a DNS response packet (%d bytes)", n)
	}
	if got := called.Load(); got != 0 {
		t.Fatalf("callback was invoked %d times for response packet", got)
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

func exchange(t *testing.T, network, addr, name string, qtype uint16) *mdns.Msg {
	t.Helper()
	m := new(mdns.Msg)
	m.SetQuestion(mdns.Fqdn(name), qtype)
	c := &mdns.Client{Net: network, Timeout: time.Second}
	deadline := time.Now().Add(3 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		resp, _, err := c.Exchange(m, addr)
		if err == nil {
			return resp
		}
		last = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s %s %s: %v", network, name, mdns.TypeToString[qtype], last)
	return nil
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
