//go:build linux

package proxy

import (
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestLinuxResolverClient is the subprocess half of
// TestLinuxResolverFindsSubprocess: it dials the listener whose address is in
// the environment and holds the connection open long enough for the parent
// test to resolve it.
func TestLinuxResolverClient(t *testing.T) {
	addr := os.Getenv("GOSPY_RESOLVE_TEST_ADDR")
	if addr == "" {
		t.Skip("subprocess-only")
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(5 * time.Second)
}

// TestLinuxResolverFindsSubprocess exercises the full /proc resolution path on
// a real kernel: an ESTABLISHED connection to the proxy listener must resolve
// to the subprocess that owns the client socket (inode -> PID -> comm/exe).
func TestLinuxResolverFindsSubprocess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(15 * time.Second)
	}()

	r := NewClientResolver(ln.Addr().String())
	defer r.Stop()

	cmd := exec.Command(os.Args[0], "-test.run=TestLinuxResolverClient")
	cmd.Env = append(os.Environ(), "GOSPY_RESOLVE_TEST_ADDR="+ln.Addr().String())
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	go cmd.Wait()

	deadline := time.Now().Add(5 * time.Second)
	var info *ProcessInfo
	for time.Now().Before(deadline) {
		for _, row := range readTCPTable() {
			if row.state != establishedState || row.remotePort != r.proxyPort || row.localPort == 0 {
				continue
			}
			info = r.resolveFromTable(row.localPort)
			if info != nil {
				break
			}
		}
		if info != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if info == nil {
		t.Fatal("resolver never resolved the subprocess client connection")
	}
	if info.PID == 0 || info.PID != uint32(cmd.Process.Pid) {
		t.Fatalf("resolved PID %d, want subprocess PID %d", info.PID, cmd.Process.Pid)
	}
	if info.Name == "" || info.Path == "" {
		t.Fatalf("incomplete process info: %+v", info)
	}
}
