//go:build linux

package netbind

import (
	"fmt"
	"syscall"
)

// bindToDevice returns a Control function that sets SO_BINDTODEVICE on the socket,
// forcing all traffic through the specified network interface.
// This is the kernel-level equivalent of `curl --interface <name>`.
//
// On Linux 5.7+, SO_BINDTODEVICE works without CAP_NET_RAW for non-raw sockets.
func bindToDevice(ifaceName string) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		var sysErr error
		err := c.Control(func(fd uintptr) {
			sysErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, ifaceName)
		})
		if err != nil {
			return fmt.Errorf("rawconn control: %w", err)
		}
		if sysErr != nil {
			return fmt.Errorf("SO_BINDTODEVICE(%s): %w", ifaceName, sysErr)
		}
		return nil
	}
}
