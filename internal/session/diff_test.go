package session

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gospy/internal/history"
)

func paramRows(t *testing.T, d *DiffResult) map[string]DiffParam {
	t.Helper()
	m := make(map[string]DiffParam)
	for _, p := range d.Params {
		if _, dup := m[p.Key]; dup {
			t.Fatalf("duplicate param row %q in diff", p.Key)
		}
		m[p.Key] = p
	}
	return m
}

func strp(s string) *string { return &s }

func TestDiffURLMatch(t *testing.T) {
	d := DiffURL(
		"https://api.example.com/v1/users?id=1&name=juan",
		"https://api.example.com/v1/users?id=1&name=juan",
		nil,
	)
	if !d.HostPath.Match {
		t.Fatalf("host+path should match: %+v", d.HostPath)
	}
	if d.DiffCount != 0 {
		t.Fatalf("expected diffCount 0, got %d", d.DiffCount)
	}
	rows := paramRows(t, d)
	if rows["id"].Status != DiffMatch || rows["name"].Status != DiffMatch {
		t.Fatalf("expected match rows, got %+v", d.Params)
	}
}

func TestDiffURLMismatchAndMissing(t *testing.T) {
	d := DiffURL(
		"https://api.example.com/v1/users?id=1&b=2",
		"https://api.example.com/v1/users?id=9&c=3",
		nil,
	)
	if !d.HostPath.Match {
		t.Fatalf("host+path should match: %+v", d.HostPath)
	}
	if d.DiffCount != 3 {
		t.Fatalf("expected diffCount 3 (id mismatch, b missing in recorded, c missing in incoming), got %d", d.DiffCount)
	}
	rows := paramRows(t, d)
	if rows["id"].Status != DiffMismatch {
		t.Fatalf("expected id mismatch, got %+v", rows["id"])
	}
	if rows["b"].Status != DiffMissingRecorded {
		t.Fatalf("expected b missing in recorded, got %+v", rows["b"])
	}
	if rows["c"].Status != DiffMissingIncoming {
		t.Fatalf("expected c missing in incoming, got %+v", rows["c"])
	}
	if rows["id"].Incoming == nil || rows["id"].Recorded == nil || *rows["id"].Incoming != "1" || *rows["id"].Recorded != "9" {
		t.Fatalf("expected id values 1/9, got %+v", rows["id"])
	}
}

func TestDiffURLHostPathMismatch(t *testing.T) {
	d := DiffURL(
		"https://a.example.com/v1?x=1",
		"https://b.example.com/v1?x=1",
		nil,
	)
	if d.HostPath.Match {
		t.Fatalf("host+path should mismatch: %+v", d.HostPath)
	}
	if d.DiffCount != 0 {
		t.Fatalf("host+path mismatch does not count params, expected 0, got %d", d.DiffCount)
	}
	if d.HostPath.Incoming != "https://a.example.com/v1" || d.HostPath.Recorded != "https://b.example.com/v1" {
		t.Fatalf("unexpected host+path: %+v", d.HostPath)
	}
}

func TestDiffURLIgnored(t *testing.T) {
	cfg := &MatchConfig{IgnoreQueryParams: []string{"token"}}
	d := DiffURL(
		"https://api.example.com/endpoint?token=abc&id=123",
		"https://api.example.com/endpoint?token=xyz&id=123",
		cfg,
	)
	if d.DiffCount != 0 {
		t.Fatalf("ignored param must not count, expected 0, got %d", d.DiffCount)
	}
	rows := paramRows(t, d)
	if rows["token"].Status != DiffIgnored {
		t.Fatalf("expected token ignored, got %+v", rows["token"])
	}
	if rows["token"].Incoming == nil || rows["token"].Recorded == nil || *rows["token"].Incoming != "abc" || *rows["token"].Recorded != "xyz" {
		t.Fatalf("expected token values abc/xyz, got %+v", rows["token"])
	}
	if rows["id"].Status != DiffMatch {
		t.Fatalf("expected id match, got %+v", rows["id"])
	}
}

func TestDiffURLEncodingConsistency(t *testing.T) {
	// %20 and + canonicalize identically once the query is re-encoded under a
	// config with ignores, so the diff must report a match.
	cfg := &MatchConfig{IgnoreQueryParams: []string{"sig"}}
	d := DiffURL(
		"https://api.example.com/x?a=hello%20world&sig=1",
		"https://api.example.com/x?a=hello+world&sig=2",
		cfg,
	)
	if d.DiffCount != 0 {
		t.Fatalf("equivalent encodings should match under cfg, got diffCount %d: %+v", d.DiffCount, d.Params)
	}
}

func TestDiffURLMultiValue(t *testing.T) {
	d := DiffURL(
		"https://api.example.com/x?tag=a&tag=b",
		"https://api.example.com/x?tag=b&tag=a",
		nil,
	)
	if d.DiffCount != 0 {
		t.Fatalf("multi-value params in different order should match, got %d", d.DiffCount)
	}
}

func TestDiffURLSchemeLess(t *testing.T) {
	d := DiffURL(
		"canalcapital.invalid/x?p=1",
		"http://canalcapital.invalid/x?p=1",
		nil,
	)
	if !d.HostPath.Match {
		t.Fatalf("scheme-less URL should normalize to http: %+v", d.HostPath)
	}
	if d.DiffCount != 0 {
		t.Fatalf("expected diffCount 0, got %d", d.DiffCount)
	}
}

// TestDiffCountAgreesWithMatchKey pins the invariant the whole match UI rests
// on: DiffCount == 0 with matching host+path is equivalent to the canonical
// match keys being equal (same method), so the diff never shows a "clean"
// candidate the queue would not have matched.
func TestDiffCountAgreesWithMatchKey(t *testing.T) {
	cfg := &MatchConfig{IgnoreQueryParams: []string{"token"}}
	cases := []struct {
		incoming, recorded string
		agree              bool
	}{
		{"https://api.example.com/x?a=1&b=2", "https://api.example.com/x?a=1&b=2", true},
		{"https://api.example.com/x?b=2&a=1", "https://api.example.com/x?a=1&b=2", true},
		{"https://api.example.com/x?a=1", "https://api.example.com/x?a=1&b=2", false},
		{"https://api.example.com/x?a=hello%20world&token=t", "https://api.example.com/x?a=hello+world&token=z", true},
		{"https://api.example.com/x?a=1", "https://api.example.com/x?a=2", false},
		{"https://api.example.com/x?a=1", "https://api.example.com/y?a=1", false},
		{"https://api.example.com/x?a=1&token=t", "https://api.example.com/x?b=2&token=t", false},
	}
	for _, c := range cases {
		d := DiffURL(c.incoming, c.recorded, cfg)
		clean := d.HostPath.Match && d.DiffCount == 0
		keysEqual := MatchKey("GET", c.incoming, cfg) == MatchKey("GET", c.recorded, cfg)
		if clean != c.agree || clean != keysEqual {
			t.Fatalf("incoming=%q recorded=%q: diff clean=%v keysEqual=%v agree=%v", c.incoming, c.recorded, clean, keysEqual, c.agree)
		}
	}
}

func TestMatchConfigRoundtrip(t *testing.T) {
	dir := t.TempDir()
	cfg := &MatchConfig{IgnoreQueryParams: []string{"token", "ts"}}
	if err := WriteMatchConfig(dir, cfg); err != nil {
		t.Fatalf("WriteMatchConfig: %v", err)
	}
	got, err := ReadMatchConfig(dir)
	if err != nil {
		t.Fatalf("ReadMatchConfig: %v", err)
	}
	if len(got.IgnoreQueryParams) != 2 || got.IgnoreQueryParams[0] != "token" || got.IgnoreQueryParams[1] != "ts" {
		t.Fatalf("unexpected roundtrip config: %+v", got)
	}
}

func TestMatchConfigMissing(t *testing.T) {
	cfg, err := ReadMatchConfig(t.TempDir())
	if err != nil {
		t.Fatalf("ReadMatchConfig on missing file: %v", err)
	}
	if cfg == nil || len(cfg.IgnoreQueryParams) != 0 {
		t.Fatalf("expected empty config, got %+v", cfg)
	}
}

func TestReplayRunWritesMatchConfig(t *testing.T) {
	rs, h, logRoot := newLoggingReplayServer(t)
	cfg := &MatchConfig{IgnoreQueryParams: []string{"sig"}}
	rs.StartNewRun(cfg)

	if err := h.Save(&history.Entry{
		ID:        "e1",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/a.m3u8", Host: "live.example.com"},
		Response:  &history.ResponseRecord{Status: 200},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	callHandleRequest(t, rs, "GET", "https://live.example.com/a.m3u8")

	summaries, err := ListReplayRuns(logRoot)
	if err != nil || len(summaries) == 0 {
		t.Fatalf("ListReplayRuns: %v (n=%d)", err, len(summaries))
	}
	dir, err := ReplayRunDir(logRoot, summaries[0].RunID)
	if err != nil {
		t.Fatalf("ReplayRunDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, matchConfigFile)); err != nil {
		t.Fatalf("match config file missing in %s: %v", dir, err)
	}
	got, err := ReadMatchConfig(dir)
	if err != nil {
		t.Fatalf("ReadMatchConfig: %v", err)
	}
	if len(got.IgnoreQueryParams) != 1 || got.IgnoreQueryParams[0] != "sig" {
		t.Fatalf("unexpected persisted config: %+v", got)
	}
}

func TestReplayServerSetOriginResolver(t *testing.T) {
	rs, h := newReplayServer(t)
	var got []string
	rs.SetOriginResolver(func(addr string) *ClientOrigin {
		got = append(got, addr)
		return &ClientOrigin{Name: "gostream.exe", Path: "C:\\bin\\gostream.exe", PID: 42}
	})
	if err := h.Save(&history.Entry{
		ID:        "e1",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/a.m3u8", Host: "live.example.com"},
		Response:  &history.ResponseRecord{Status: 200},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	resp := callHandleRequest(t, rs, "GET", "https://live.example.com/a.m3u8")
	if resp == nil {
		t.Fatal("nil response")
	}
	if len(got) == 0 {
		t.Fatal("origin resolver was never invoked")
	}
}

func TestDiffURLHostRules(t *testing.T) {
	incoming := "https://live.example.com/hls/master.m3u8?uid=123&ver=2&ts=99"
	recorded := "https://live.example.com/hls/master.m3u8?uid=456&ver=2&ts=99"
	cfg := &MatchConfig{
		HostRules: []HostRule{
			{Host: "live.example.com", PathPrefix: "/hls/", IgnoreQueryParams: []string{"uid"}},
		},
	}
	diff := DiffURL(incoming, recorded, cfg)
	if diff.HostPath.Match != true {
		t.Errorf("host_rules diff: HostPath.Match = false, want true")
	}
	if diff.DiffCount != 0 {
		t.Errorf("host_rules diff: DiffCount = %d, want 0", diff.DiffCount)
	}
	// uid should appear as DiffIgnored, ver as DiffMatch
	foundUID := false
	foundVer := false
	for _, p := range diff.Params {
		if p.Key == "uid" && p.Status == DiffIgnored {
			foundUID = true
		}
		if p.Key == "ver" && p.Status == DiffMatch {
			foundVer = true
		}
	}
	if !foundUID {
		t.Error("host_rules diff: uid should be DiffIgnored")
	}
	if !foundVer {
		t.Error("host_rules diff: ver should be DiffMatch")
	}
}

func TestDiffURLHostRulesNoMatch(t *testing.T) {
	incoming := "https://other.example.com/hls/master.m3u8?uid=123"
	recorded := "https://other.example.com/hls/master.m3u8?uid=456"
	cfg := &MatchConfig{
		HostRules: []HostRule{
			{Host: "live.example.com", PathPrefix: "/hls/", IgnoreQueryParams: []string{"uid"}},
		},
	}
	diff := DiffURL(incoming, recorded, cfg)
	// Non-matching host_rule: uid should NOT be ignored — it should be DiffMismatch
	for _, p := range diff.Params {
		if p.Key == "uid" {
			if p.Status != DiffMismatch {
				t.Errorf("non-matching host_rule: uid should mismatch, got %v", p.Status)
			}
			return
		}
	}
	t.Error("uid param not found in diff")
}

func TestReplayIgnoredHost(t *testing.T) {
	rs, h := newReplayServer(t)
	if err := h.Save(&history.Entry{
		ID:        "e1",
		Timestamp: time.Now(),
		Request:   history.RequestRecord{Method: "GET", URL: "https://live.example.com/hls/master.m3u8", Host: "live.example.com"},
		Response:  &history.ResponseRecord{Status: 200},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Override match config with ignore_hosts.
	rs.cfg = &MatchConfig{IgnoreHosts: []string{"live.example.com"}}

	resp := callHandleRequest(t, rs, "GET", "https://live.example.com/hls/master.m3u8")
	if resp == nil {
		t.Fatal("nil response")
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Gospy-Replay"); got != "ignored" {
		t.Errorf("X-Gospy-Replay = %q, want %q", got, "ignored")
	}
}
