package ca

import "net"

func parseIP(host string) net.IP {
	return net.ParseIP(host)
}
