// Package dns answers wha.internal from the address the platform passes to Serve.
// It does not look up Docker and it does not invent addresses. The records
// callback is how the platform publishes the stable tunnel address.
package dns

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	mdns "github.com/miekg/dns"
)

const (
	zone = "wha.internal"

	// ttlA is the TTL of address answers. Negative answers use the SOA minimum
	// below so a name looked up just before it exists is not cached for long.
	ttlA   = 5
	soaTTL = 1
	soaMin = 1

	ednsUDP = 1232
)

// Serve answers DNS for wha.internal on addr, over UDP and TCP, until ctx is
// cancelled. A cancelled context stops both listeners and returns nil.
//
// records may be called concurrently. Its name is the lowercased query name
// without a trailing dot, for example "web.wha.internal". When it reports
// ok false, the name is NXDOMAIN, except the zone apex, which answers with no
// data. Names outside the zone are refused and
// records is not called. A queries are answered with the callback's IPv4
// addresses at a TTL of 5 seconds. Negative answers carry an SOA whose
// minimum is 1 second.
func Serve(ctx context.Context, addr string, records func(name string) (ips []net.IP, ok bool)) error {
	if records == nil {
		return errors.New("dns: nil records callback")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	h := mdns.HandlerFunc(func(w mdns.ResponseWriter, r *mdns.Msg) {
		answer(w, r, records)
	})
	udpReady := make(chan struct{})
	tcpReady := make(chan struct{})
	udp := &mdns.Server{
		Addr:              addr,
		Net:               "udp",
		Handler:           h,
		NotifyStartedFunc: func() { close(udpReady) },
	}
	tcp := &mdns.Server{
		Addr:              addr,
		Net:               "tcp",
		Handler:           h,
		NotifyStartedFunc: func() { close(tcpReady) },
	}

	udpExited := make(chan struct{})
	tcpExited := make(chan struct{})
	errCh := make(chan error, 2)
	go func() {
		defer close(udpExited)
		errCh <- udp.ListenAndServe()
	}()
	go func() {
		defer close(tcpExited)
		errCh <- tcp.ListenAndServe()
	}()

	udpDone := udpReady
	tcpDone := tcpReady
	started := 0
	for started < 2 {
		select {
		case <-ctx.Done():
			shutdownServer(udp, udpExited)
			shutdownServer(tcp, tcpExited)
			return nil
		case err := <-errCh:
			shutdownServer(udp, udpExited)
			shutdownServer(tcp, tcpExited)
			if err != nil {
				return err
			}
			return errors.New("dns: server stopped before it was listening")
		case <-udpDone:
			udpDone = nil
			started++
		case <-tcpDone:
			tcpDone = nil
			started++
		}
	}

	select {
	case <-ctx.Done():
		shutdownServer(udp, udpExited)
		shutdownServer(tcp, tcpExited)
		return nil
	case err := <-errCh:
		shutdownServer(udp, udpExited)
		shutdownServer(tcp, tcpExited)
		if err != nil {
			return err
		}
		return nil
	}
}

func shutdownServer(s *mdns.Server, exited <-chan struct{}) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		select {
		case <-exited:
			return
		default:
		}
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		err := s.ShutdownContext(ctx)
		cancel()
		if err == nil || time.Now().After(deadline) {
			return
		}
		select {
		case <-exited:
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func answer(w mdns.ResponseWriter, r *mdns.Msg, records func(string) ([]net.IP, bool)) {
	if r.Response {
		return
	}
	m := new(mdns.Msg)
	m.SetReply(r)
	m.Compress = true

	if r.Opcode != mdns.OpcodeQuery || len(r.Question) != 1 {
		m.Rcode = mdns.RcodeRefused
		writeMsg(w, r, m)
		return
	}
	q := r.Question[0]
	name := normalize(q.Name)
	if q.Qclass != mdns.ClassINET || !inZone(name) {
		m.Rcode = mdns.RcodeRefused
		writeMsg(w, r, m)
		return
	}

	m.Authoritative = true
	ips, ok := records(name)
	// The apex always exists. NXDOMAIN there would let a resolver that
	// follows RFC 8020 treat every name in the zone as nonexistent.
	if !ok && name != zone {
		m.Rcode = mdns.RcodeNameError
		m.Ns = []mdns.RR{zoneSOA()}
		writeMsg(w, r, m)
		return
	}
	if name == zone && (q.Qtype == mdns.TypeSOA || q.Qtype == mdns.TypeANY) {
		m.Answer = append(m.Answer, zoneSOA())
	}
	if ok && (q.Qtype == mdns.TypeA || q.Qtype == mdns.TypeANY) {
		for _, ip := range ips {
			if ip == nil {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			m.Answer = append(m.Answer, &mdns.A{
				Hdr: mdns.RR_Header{
					Name:   q.Name,
					Rrtype: mdns.TypeA,
					Class:  mdns.ClassINET,
					Ttl:    ttlA,
				},
				A: ip4,
			})
		}
	}
	if len(m.Answer) == 0 {
		// The name exists and this query has no data (AAAA, or an A query
		// whose callback returned no IPv4). NODATA, not NXDOMAIN.
		m.Ns = []mdns.RR{zoneSOA()}
	}
	writeMsg(w, r, m)
}

func writeMsg(w mdns.ResponseWriter, r, m *mdns.Msg) {
	size := mdns.MinMsgSize
	if opt := r.IsEdns0(); opt != nil {
		m.SetEdns0(ednsUDP, false)
		if s := int(opt.UDPSize()); s > size {
			size = min(s, ednsUDP)
		}
	}
	if _, udp := w.RemoteAddr().(*net.UDPAddr); udp {
		m.Truncate(size)
	}
	_ = w.WriteMsg(m)
}

func normalize(name string) string {
	name = strings.TrimSpace(name)
	return strings.TrimSuffix(strings.ToLower(name), ".")
}

func inZone(name string) bool {
	if name == zone {
		return true
	}
	if !strings.HasSuffix(name, "."+zone) {
		return false
	}
	prefix := strings.TrimSuffix(name, "."+zone)
	return prefix != "" && !strings.HasPrefix(prefix, ".") && !strings.HasSuffix(prefix, ".") && !strings.Contains(prefix, "..")
}

func zoneSOA() *mdns.SOA {
	return &mdns.SOA{
		Hdr: mdns.RR_Header{
			Name:   mdns.Fqdn(zone),
			Rrtype: mdns.TypeSOA,
			Class:  mdns.ClassINET,
			Ttl:    soaTTL,
		},
		Ns:      mdns.Fqdn("ns." + zone),
		Mbox:    mdns.Fqdn("hostmaster." + zone),
		Serial:  1,
		Refresh: 60,
		Retry:   60,
		Expire:  600,
		Minttl:  soaMin,
	}
}
