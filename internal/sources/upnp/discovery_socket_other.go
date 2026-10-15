//go:build !unix

package upnp

import "net"

// Other systems use the socket's bound source address and default multicast route.
func setDiscoveryInterface(_ *net.UDPConn, _ net.IP) error { return nil }
