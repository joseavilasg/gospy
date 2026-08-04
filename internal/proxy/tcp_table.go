package proxy

import (
	"net"
	"strconv"
	"strings"
)

const establishedState = 0x01

// tcpRow is one row of /proc/net/tcp or /proc/net/tcp6 reduced to what the
// linux client resolver needs: the connection's local/remote ports, its state,
// and the kernel socket inode backing the local end.
type tcpRow struct {
	localPort  uint16
	remotePort uint16
	state      uint8
	inode      uint64
}

// parseTCPTable parses /proc/net/tcp[6] text. Addresses are hex "IP:port"; the
// fields the resolver needs (ports, state, inode) are parsed and the rest is
// ignored. Rows that don't parse cleanly are skipped.
func parseTCPTable(data []byte) []tcpRow {
	var rows []tcpRow
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		localPort, ok := hexPort(fields[1])
		if !ok {
			continue
		}
		remotePort, ok := hexPort(fields[2])
		if !ok {
			continue
		}
		state, err := strconv.ParseUint(fields[3], 16, 8)
		if err != nil {
			continue
		}
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			continue
		}
		rows = append(rows, tcpRow{
			localPort:  localPort,
			remotePort: remotePort,
			state:      uint8(state),
			inode:      inode,
		})
	}
	return rows
}

// hexPort parses the port out of a hex "IP:port" address (e.g. "0100007F:1F90").
func hexPort(addr string) (uint16, bool) {
	i := strings.LastIndexByte(addr, ':')
	if i < 0 || i == len(addr)-1 {
		return 0, false
	}
	n, err := strconv.ParseUint(addr[i+1:], 16, 16)
	if err != nil {
		return 0, false
	}
	return uint16(n), true
}

// clientInodes maps the local ephemeral port of every ESTABLISHED connection
// whose remote end is the proxy to the kernel inode of the client socket. Those
// are the sockets the proxy forwards for, and the inode is what links the port
// back to the owning process via /proc/<pid>/fd.
func clientInodes(rows []tcpRow, proxyPort uint16) map[uint16]uint64 {
	out := make(map[uint16]uint64)
	for _, row := range rows {
		if row.state != establishedState {
			continue
		}
		if row.remotePort != proxyPort || row.localPort == 0 {
			continue
		}
		out[row.localPort] = row.inode
	}
	return out
}

// parsePort extracts the numeric port from an address string ("host:port").
func parsePort(addr string) uint16 {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return 0
	}
	return uint16(n)
}
