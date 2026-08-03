//go:build !linux

package netbind

import "syscall"

// bindToDevice returns nil on non-Linux platforms where SO_BINDTODEVICE is not available.
// The caller falls back to LocalAddr binding (source IP only).
func bindToDevice(_ string) func(network, address string, c syscall.RawConn) error {
	return nil
}
