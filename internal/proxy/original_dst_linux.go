//go:build linux

package proxy

import (
	"encoding/binary"
	"errors"
	"net"
	"syscall"
	"unsafe"
)

// soOriginalDst is the Linux netfilter socket option that returns the
// destination address from before an iptables REDIRECT.
const soOriginalDst = 80

func readOriginalDestination(conn net.Conn) (net.IP, error) {
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return nil, errors.New("proxy: not a TCP connection")
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		return nil, err
	}
	var sa syscall.RawSockaddrInet4
	size := uint32(unsafe.Sizeof(sa))
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		_, _, errno := syscall.Syscall6(
			syscall.SYS_GETSOCKOPT,
			fd,
			uintptr(syscall.IPPROTO_IP),
			uintptr(soOriginalDst),
			uintptr(unsafe.Pointer(&sa)),
			uintptr(unsafe.Pointer(&size)),
			0,
		)
		if errno != 0 {
			sockErr = errno
		}
	}); err != nil {
		return nil, err
	}
	if sockErr != nil {
		return nil, sockErr
	}
	if size != uint32(unsafe.Sizeof(sa)) || sa.Family != syscall.AF_INET {
		return nil, errors.New("proxy: original destination is not IPv4")
	}
	origIP := net.IPv4(sa.Addr[0], sa.Addr[1], sa.Addr[2], sa.Addr[3])
	if origIP.IsUnspecified() {
		return nil, errors.New("proxy: original destination is unspecified")
	}
	portBytes := (*[2]byte)(unsafe.Pointer(&sa.Port))
	origPort := int(binary.BigEndian.Uint16(portBytes[:]))
	if local, ok := conn.LocalAddr().(*net.TCPAddr); ok && origIP.Equal(local.IP) && origPort == local.Port {
		return nil, errors.New("proxy: connection was not redirected")
	}
	return origIP, nil
}
