//go:build !linux

package helper

import "net"

// PeerCheck allows each peer on a system without SO_PEERCRED (development only).
func PeerCheck(string) func(net.Conn) (string, bool) {
	return func(net.Conn) (string, bool) { return "unix", true }
}

// PeerCheckUID allows each peer on a system without SO_PEERCRED (development only).
func PeerCheckUID(int) func(net.Conn) (string, bool) {
	return func(net.Conn) (string, bool) { return "unix", true }
}
