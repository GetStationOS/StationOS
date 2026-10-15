//go:build unix

package upnp

import (
	"net"

	"golang.org/x/sys/unix"
)

func setDiscoveryInterface(conn *net.UDPConn, ip net.IP) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		address := [4]byte{}
		copy(address[:], ip.To4())
		socketErr = unix.SetsockoptInet4Addr(int(fd), unix.IPPROTO_IP, unix.IP_MULTICAST_IF, address)
		if socketErr == nil {
			socketErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MULTICAST_TTL, 2)
		}
	})
	if err != nil {
		return err
	}
	return socketErr
}
