package proxy

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
)

// Decision is the TCP 443 probe result. Redirect is true only when 443 is
// closed and an HTTP port accepts a TCP connection.
type Decision struct {
	Redirect bool
	HTTPPort int
}

// Ports Probe dials. Tests point them at unprivileged ports; production uses
// 443 and then 80, 8080, 3000, 5000, 8000.
var (
	probeHTTPSPort = 443
	probeHTTPPorts = []int{80, 8080, 3000, 5000, 8000}
)

// dialTimeout is how long each TCP connect waits when ctx has no sooner deadline.
const dialTimeout = 500 * time.Millisecond

// Probe connects to bridgeIP:443. A successful TCP connect means the target
// already accepts TLS, so Redirect is false and no HTTP port is chosen.
// Otherwise it tries httpPortLabel when that label is a port from 1 to 65535,
// or else 80, 8080, 3000, 5000, and 8000, in that order. The first successful
// TCP connect sets Redirect and HTTPPort. If none connect, Redirect is false
// and HTTPPort is 0. Probe only tests TCP connects; it does not send HTTP or TLS.
func Probe(ctx context.Context, bridgeIP, httpPortLabel string) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	bridgeIP = strings.TrimSpace(bridgeIP)
	if bridgeIP == "" {
		return Decision{}, errors.New("proxy: empty bridge address")
	}
	if !validBridgeIP(bridgeIP) {
		return Decision{}, errors.New("proxy: bridge address is not a valid unicast IP address")
	}
	open, err := tcpAccepts(ctx, bridgeIP, probeHTTPSPort)
	if err != nil {
		return Decision{}, err
	}
	if open {
		return Decision{}, nil
	}

	ports := append([]int(nil), probeHTTPPorts...)
	if port, ok := parsePort(httpPortLabel); ok {
		if port == probeHTTPSPort {
			return Decision{}, nil
		}
		ports = []int{port}
	}
	for _, port := range ports {
		if port == probeHTTPSPort {
			continue
		}
		open, err = tcpAccepts(ctx, bridgeIP, port)
		if err != nil {
			return Decision{}, err
		}
		if open {
			return Decision{Redirect: true, HTTPPort: port}, nil
		}
	}
	return Decision{}, nil
}

func validBridgeIP(raw string) bool {
	ip := net.ParseIP(raw)
	return ip != nil && !ip.IsUnspecified() && !ip.IsMulticast()
}

func parsePort(label string) (int, bool) {
	label = strings.TrimSpace(label)
	if label == "" {
		return 0, false
	}
	n, err := strconv.Atoi(label)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != label {
		return 0, false
	}
	return n, true
}

func tcpAccepts(ctx context.Context, host string, port int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	_ = conn.Close()
	return true, nil
}
