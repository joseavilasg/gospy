package proxy

import "testing"

func TestParseTCPTable(t *testing.T) {
	data := "" +
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:1F90 0100007F:E1C4 01 00000000:00000000 00:00000000 00000000  1000        0 123456 1 0000000000000000 100 0 0 10 0\n" +
		"   1: 0100007F:E1C4 0100007F:1F90 01 00000000:00000000 00:00000000 00000000  1000        0 654321 1 0000000000000000 100 0 0 10 0\n" +
		"   2: 0100007F:1F90 0100007F:E1C5 06 00000000:00000000 00:00000000 00000000  1000        0 111111 1 0000000000000000 100 0 0 10 0\n" +
		"   3: 00000000000000000000000000000000:1F90 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 999999 1 0000000000000000 100 0 0 10 0\n"

	rows := parseTCPTable([]byte(data))
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(rows))
	}

	if rows[0].localPort != 8080 || rows[0].remotePort != 57796 || rows[0].state != 0x01 || rows[0].inode != 123456 {
		t.Errorf("row0 = %+v, want {local 8080 remote 57796 state 01 inode 123456}", rows[0])
	}
	if rows[1].localPort != 57796 || rows[1].remotePort != 8080 || rows[1].inode != 654321 {
		t.Errorf("row1 = %+v, want {local 57796 remote 8080 inode 654321}", rows[1])
	}
	if rows[2].state != 0x06 || rows[2].inode != 111111 {
		t.Errorf("row2 = %+v, want state 06 inode 111111", rows[2])
	}
	if rows[3].localPort != 8080 || rows[3].remotePort != 0 || rows[3].state != 0x0A {
		t.Errorf("row3 = %+v, want {local 8080 remote 0 state 0A}", rows[3])
	}
}

func TestParseTCPTableSkipsGarbage(t *testing.T) {
	data := "" +
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"garbage line that is not a row\n" +
		"   0: 0100007F:XYZ1 0100007F:E1C4 01 00000000:00000000 00:00000000 00000000  1000        0 1 1 0000000000000000 100 0 0 10 0\n" +
		"   1: 0100007F:1F90 0100007F: 01 00000000:00000000 00:00000000 00000000  1000        0 2 1 0000000000000000 100 0 0 10 0\n" +
		"   2: 0100007F:1F90 0100007F:E1C4 ZZ 00000000:00000000 00:00000000 00000000  1000        0 3 1 0000000000000000 100 0 0 10 0\n" +
		"   3: 0100007F:1F90 0100007F:E1C4 01 00000000:00000000 00:00000000 00000000  1000        0 NOTINODE 1 0000000000000000 100 0 0 10 0\n" +
		"   4: 0100007F:1F90 0100007F:E1C4 01 00000000\n"

	if rows := parseTCPTable([]byte(data)); len(rows) != 0 {
		t.Fatalf("rows = %d, want 0 (got %+v)", len(rows), rows)
	}
}

func TestClientInodes(t *testing.T) {
	rows := []tcpRow{
		{localPort: 57796, remotePort: 8080, state: establishedState, inode: 654321},
		{localPort: 8080, remotePort: 57796, state: establishedState, inode: 123456},
		{localPort: 50000, remotePort: 8080, state: 0x06, inode: 111},
		{localPort: 0, remotePort: 8080, state: establishedState, inode: 222},
	}

	got := clientInodes(rows, 8080)
	if len(got) != 1 || got[57796] != 654321 {
		t.Fatalf("clientInodes = %v, want {57796:654321}", got)
	}
}

func TestParsePort(t *testing.T) {
	cases := []struct {
		addr string
		want uint16
	}{
		{"127.0.0.1:8080", 8080},
		{"[::1]:8443", 8443},
		{"127.0.0.1", 0},
		{"", 0},
		{":", 0},
		{"host:70000", 0},
	}
	for _, c := range cases {
		if got := parsePort(c.addr); got != c.want {
			t.Errorf("parsePort(%q) = %d, want %d", c.addr, got, c.want)
		}
	}
}
