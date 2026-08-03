// Package netbind builds a *net.Dialer bound to a specific network interface
// (e.g., a WireGuard VPN) and/or using a custom DNS resolver.
//
// On Linux, interface binding uses SO_BINDTODEVICE - the same mechanism as
// `curl --interface <iface>`. This forces ALL socket traffic (including routing)
// through the specified interface, not just the source IP. On other platforms it
// falls back to LocalAddr binding (source IP only).
//
// When the interface is set without an explicit DNS server, the DNS server is
// auto-detected via `resolvectl dns <iface>` so queries bypass the system's
// global DNS and use the VPN's own DNS, routed through SO_BINDTODEVICE.
//
// This mirrors the netbind package of gostream, so the proxy can reach
// region-locked providers whose VPN interface is app-bound (only processes
// bound to the interface exit through it).
package netbind

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/zap"
)

// BuildDialer returns a *net.Dialer bound to the given network interface and/or
// DNS server. Returns (nil, nil) when both ifaceName and dnsServer are empty.
//
// The interface must exist on the host: if it does not, an error is returned so
// callers can fail fast instead of discovering the problem on every dial.
func BuildDialer(ifaceName, dnsServer string, logger *zap.SugaredLogger) (*net.Dialer, error) {
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}

	ifaceName = strings.TrimSpace(ifaceName)
	dnsServer = strings.TrimSpace(dnsServer)

	if ifaceName == "" && dnsServer == "" {
		return nil, nil
	}

	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	// Bind to network interface
	if ifaceName != "" {
		// Fail early if the interface does not exist, on any platform.
		if _, err := net.InterfaceByName(ifaceName); err != nil {
			return nil, fmt.Errorf("netbind: interface %q not found: %w", ifaceName, err)
		}

		// Set SO_BINDTODEVICE (Linux) to force all traffic through the interface.
		// This is the kernel-level equivalent of `curl --interface`.
		controlFn := bindToDevice(ifaceName)
		if controlFn != nil {
			dialer.Control = controlFn
			logger.Info("Network interface bound via SO_BINDTODEVICE",
				zap.String("interface", ifaceName))
		} else {
			// Fallback: set source IP (non-Linux or SO_BINDTODEVICE unavailable)
			ip, err := getInterfaceIPv4(ifaceName)
			if err != nil {
				return nil, fmt.Errorf("netbind: failed to get IP for interface %q: %w", ifaceName, err)
			}
			dialer.LocalAddr = &net.TCPAddr{IP: ip}
			logger.Info("Network interface bound via LocalAddr (fallback)",
				zap.String("interface", ifaceName),
				zap.String("ip", ip.String()))
		}
	}

	// Custom DNS resolver - when dnsServer is explicitly set, or auto-detected
	// from the interface via resolvectl. This bypasses the system's global DNS
	// and routes queries through the VPN's own DNS.
	if dnsServer == "" && ifaceName != "" {
		// Auto-detect DNS from interface (e.g., WireGuard via resolvectl)
		detected, err := detectDNSForInterface(ifaceName)
		if err != nil {
			logger.Warn("Failed to auto-detect DNS for interface, using system DNS",
				zap.String("interface", ifaceName),
				zap.Error(err))
		} else {
			dnsServer = detected
			logger.Info("DNS auto-detected for interface",
				zap.String("interface", ifaceName),
				zap.String("dns", dnsServer))
		}
	}

	if dnsServer != "" {
		dnsAddr := dnsServer + ":53"
		dialer.Resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: 5 * time.Second}
				// If we have SO_BINDTODEVICE, apply it to DNS queries too
				if ifaceName != "" {
					d.Control = bindToDevice(ifaceName)
				}
				return d.DialContext(ctx, "udp", dnsAddr)
			},
		}
		logger.Info("Custom DNS resolver configured",
			zap.String("dns", dnsServer))
	}

	return dialer, nil
}

// getInterfaceIPv4 returns the first IPv4 address assigned to the named interface.
func getInterfaceIPv4(ifaceName string) (net.IP, error) {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return nil, fmt.Errorf("interface %q not found: %w", ifaceName, err)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return nil, fmt.Errorf("failed to get addresses for %q: %w", ifaceName, err)
	}

	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}

		if ip != nil && ip.To4() != nil {
			return ip.To4(), nil
		}
	}

	return nil, fmt.Errorf("no IPv4 address found on interface %q", ifaceName)
}

// detectDNSForInterface auto-detects the DNS server for an interface using resolvectl.
// This works for WireGuard and any VPN that registers DNS with systemd-resolved.
//
// Example: resolvectl dns cls1 → "Link 4 (cls1): 10.64.0.1"
func detectDNSForInterface(ifaceName string) (string, error) {
	out, err := exec.Command("resolvectl", "dns", ifaceName).Output()
	if err != nil {
		return "", fmt.Errorf("resolvectl failed: %w", err)
	}
	return parseResolvectlOutput(string(out))
}

// parseResolvectlOutput extracts the first DNS server IP from resolvectl output.
func parseResolvectlOutput(line string) (string, error) {
	line = strings.TrimSpace(line)
	parts := strings.SplitN(line, ":", 2)
	if len(parts) < 2 {
		return "", fmt.Errorf("unexpected resolvectl output: %q", line)
	}

	dns := strings.TrimSpace(parts[1])
	if dns == "" {
		return "", fmt.Errorf("no DNS found in resolvectl output for %q", line)
	}

	// Take only the first DNS if multiple are listed (space-separated)
	if fields := strings.Fields(dns); len(fields) > 0 {
		dns = fields[0]
	}

	// Validate it's a valid IP
	if net.ParseIP(dns) == nil {
		return "", fmt.Errorf("invalid DNS IP %q from resolvectl", dns)
	}

	return dns, nil
}
