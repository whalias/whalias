//go:build !linux

package proxy

import (
	"errors"
	"net"
)

func readOriginalDestination(conn net.Conn) (net.IP, error) {
	return nil, errors.New("proxy: original destination is only available on linux")
}
