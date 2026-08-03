package netbind

import (
	"net"
	"testing"
)

func TestBuildDialer_Empty(t *testing.T) {
	d, err := BuildDialer("", "", nil)
	if err != nil {
		t.Fatalf("BuildDialer(empty) error: %v", err)
	}
	if d != nil {
		t.Fatalf("BuildDialer(empty) = %v, want nil", d)
	}
}

func TestBuildDialer_InvalidInterface(t *testing.T) {
	_, err := BuildDialer("gospy-no-such-interface", "", nil)
	if err == nil {
		t.Fatal("BuildDialer(invalid iface) = nil error, want error")
	}
}

func pickInterface(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("net.Interfaces: %v", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return iface.Name
			}
		}
	}
	t.Skip("no up interface with IPv4 available")
	return ""
}

func TestBuildDialer_ValidInterface(t *testing.T) {
	name := pickInterface(t)
	d, err := BuildDialer(name, "", nil)
	if err != nil {
		t.Fatalf("BuildDialer(%q) error: %v", name, err)
	}
	if d == nil {
		t.Fatalf("BuildDialer(%q) = nil dialer", name)
	}
	if d.Control == nil && d.LocalAddr == nil {
		t.Fatalf("BuildDialer(%q): neither Control nor LocalAddr bound", name)
	}
}

func TestBuildDialer_CustomDNS(t *testing.T) {
	d, err := BuildDialer("", "8.8.8.8", nil)
	if err != nil {
		t.Fatalf("BuildDialer(dns) error: %v", err)
	}
	if d == nil || d.Resolver == nil {
		t.Fatalf("BuildDialer(dns): Resolver not configured")
	}
}

func TestBuildDialer_InterfaceAndDNS(t *testing.T) {
	name := pickInterface(t)
	d, err := BuildDialer(name, "1.1.1.1", nil)
	if err != nil {
		t.Fatalf("BuildDialer(iface,dns) error: %v", err)
	}
	if d == nil || d.Resolver == nil {
		t.Fatal("BuildDialer(iface,dns): Resolver not configured")
	}
}

func TestParseResolvectlOutput(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Link 4 (cls1): 10.64.0.1", "10.64.0.1"},
		{"Link 4 (cls1): 10.64.0.1 1.1.1.1", "10.64.0.1"},
		{"Link 4 (cls1): fd00::1", "fd00::1"},
		{"Link 4 (cls1): ", ""},
		{"garbage", ""},
		{"Link 4: not-an-ip", ""},
	}
	for _, c := range cases {
		got, err := parseResolvectlOutput(c.in)
		if c.want == "" {
			if err == nil {
				t.Errorf("parseResolvectlOutput(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseResolvectlOutput(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseResolvectlOutput(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
