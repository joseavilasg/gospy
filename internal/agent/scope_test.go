package agent

import (
	"testing"

	"gospy/internal/history"
)

type mockFilterStore struct {
	gate         bool
	filters      history.Filters
	focusEnabled bool
}

func (m *mockFilterStore) SnapshotAgent() (history.Filters, bool, int) {
	return m.filters, m.focusEnabled, 1
}

func (m *mockFilterStore) AgentGate() bool { return m.gate }

type testHostMatcher struct {
	hosts []string
}

func (m *testHostMatcher) Matches(host string) bool {
	for _, h := range m.hosts {
		if h == host {
			return true
		}
	}
	return false
}

func newTestHistory(t *testing.T) *history.Store {
	t.Helper()
	hist, err := history.New(t.TempDir() + "/history")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	return hist
}

func saveTestEntry(t *testing.T, hist *history.Store, host, origin string, status int) *history.Entry {
	t.Helper()
	e := &history.Entry{
		Request: history.RequestRecord{
			Method:  "GET",
			URL:     "http://" + host + "/path",
			Host:    host,
			Headers: map[string][]string{},
		},
		Response: &history.ResponseRecord{Status: status},
		Origin:   origin,
	}
	if err := hist.Save(e); err != nil {
		t.Fatalf("save: %v", err)
	}
	return e
}

func TestScope_GateOffIsEmpty(t *testing.T) {
	hist := newTestHistory(t)
	saveTestEntry(t, hist, "a.com", "", 200)
	sc := NewScope(hist, &mockFilterStore{gate: false}, nil, nil)

	page := sc.ListEntries(history.Filters{}, 0, 10)
	if len(page.Entries) != 0 || page.VisibleCount != 0 {
		t.Fatalf("gate off must expose nothing, got %+v", page)
	}
	if page.Entries == nil {
		t.Fatal("empty entries must serialize as [], not null")
	}
	if sc.IsVisible("whatever") {
		t.Error("IsVisible must be false with gate off")
	}
	if hosts := sc.VisibleHosts(); len(hosts) != 0 {
		t.Errorf("VisibleHosts with gate off = %v", hosts)
	}
}

func TestScope_CriteriaFilter(t *testing.T) {
	hist := newTestHistory(t)
	saveTestEntry(t, hist, "a.com", "", 200)
	saveTestEntry(t, hist, "b.com", "", 200)
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"a.com"}, MatchMode: "all"}}
	sc := NewScope(hist, fs, nil, nil)

	page := sc.ListEntries(history.Filters{}, 0, 10)
	if len(page.Entries) != 1 || page.Entries[0].Host != "a.com" {
		t.Fatalf("expected only a.com, got %+v", page.Entries)
	}
	if page.VisibleCount != 1 || page.Total != 2 {
		t.Errorf("visibleCount = %d (want 1), total = %d (want 2)", page.VisibleCount, page.Total)
	}
	if page.HasMore {
		t.Error("hasMore must be false")
	}
}

func TestScope_AgentOriginFiltered(t *testing.T) {
	hist := newTestHistory(t)
	agentEntry := saveTestEntry(t, hist, "a.com", "agent", 200)
	saveTestEntry(t, hist, "a.com", "", 200)
	saveTestEntry(t, hist, "b.com", "", 200)
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"b.com"}, MatchMode: "all"}}
	sc := NewScope(hist, fs, nil, nil)

	page := sc.ListEntries(history.Filters{}, 0, 10)
	if len(page.Entries) != 1 || page.Entries[0].Host != "b.com" {
		t.Fatalf("expected only b.com: the agent-origin a.com entry must be filtered like any other, got %+v", page.Entries)
	}
	if page.VisibleCount != 1 {
		t.Errorf("visibleCount = %d, want 1", page.VisibleCount)
	}
	if sc.IsVisible(agentEntry.ID) {
		t.Error("agent-origin entry outside the filters must not be visible")
	}
}

func TestScope_IgnoreStaysHardPrefilter(t *testing.T) {
	hist := newTestHistory(t)
	e := saveTestEntry(t, hist, "ignored.com", "agent", 200)
	sc := NewScope(hist, &mockFilterStore{gate: true}, &testHostMatcher{hosts: []string{"ignored.com"}}, nil)

	if sc.IsVisible(e.ID) {
		t.Error("an ignored host must stay invisible regardless of origin")
	}
	if page := sc.ListEntries(history.Filters{}, 0, 10); len(page.Entries) != 0 {
		t.Errorf("ignored entry leaked into the page: %+v", page.Entries)
	}
}

func TestScope_Pagination(t *testing.T) {
	hist := newTestHistory(t)
	for i := 0; i < 3; i++ {
		saveTestEntry(t, hist, "a.com", "", 200)
	}
	sc := NewScope(hist, &mockFilterStore{gate: true}, nil, nil)

	first := sc.ListEntries(history.Filters{}, 0, 2)
	if len(first.Entries) != 2 || !first.HasMore || first.VisibleCount != 3 {
		t.Fatalf("first page = %+v", first)
	}
	second := sc.ListEntries(history.Filters{}, 2, 2)
	if len(second.Entries) != 1 || second.HasMore {
		t.Fatalf("second page = %+v", second)
	}
	// Offsets past the end return an empty, non-nil slice.
	past := sc.ListEntries(history.Filters{}, 99, 2)
	if len(past.Entries) != 0 || past.Entries == nil {
		t.Fatalf("offset past end = %+v", past.Entries)
	}
	// pageLimits: default 50, hard cap 200, non-negative offset.
	cases := []struct{ off, lim, wantOff, wantLim int }{
		{0, 0, 0, 50},
		{0, -5, 0, 50},
		{-3, 10, 0, 10},
		{0, 500, 0, 200},
	}
	for _, c := range cases {
		off, lim := pageLimits(c.off, c.lim)
		if off != c.wantOff || lim != c.wantLim {
			t.Errorf("pageLimits(%d,%d) = (%d,%d), want (%d,%d)", c.off, c.lim, off, lim, c.wantOff, c.wantLim)
		}
	}
}

func TestScope_IsVisibleAndHosts(t *testing.T) {
	hist := newTestHistory(t)
	a := saveTestEntry(t, hist, "a.com", "", 200)
	b := saveTestEntry(t, hist, "b.com", "", 200)
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"a.com"}, MatchMode: "all"}}
	sc := NewScope(hist, fs, nil, nil)

	if !sc.IsVisible(a.ID) {
		t.Error("a.com entry must be visible")
	}
	if sc.IsVisible(b.ID) {
		t.Error("b.com entry must not be visible")
	}
	if !sc.GateEnabled() {
		t.Error("GateEnabled must report the gate")
	}

	saveTestEntry(t, hist, "a.com", "", 200)
	hosts := sc.VisibleHosts()
	if len(hosts) != 1 || hosts[0] != "a.com" {
		t.Errorf("VisibleHosts = %v, want [a.com]", hosts)
	}
}

func saveTestEntryFull(t *testing.T, hist *history.Store, method, host, path string, status int) *history.Entry {
	t.Helper()
	e := &history.Entry{
		Request: history.RequestRecord{
			Method:  method,
			URL:     "http://" + host + path,
			Host:    host,
			Headers: map[string][]string{},
		},
		Response: &history.ResponseRecord{Status: status},
	}
	if err := hist.Save(e); err != nil {
		t.Fatalf("save: %v", err)
	}
	return e
}

func TestScope_QueryNarrowsProfile(t *testing.T) {
	hist := newTestHistory(t)
	saveTestEntryFull(t, hist, "GET", "sonarcloud.io", "/api/issues/search", 200)
	saveTestEntryFull(t, hist, "GET", "sonarcloud.io", "/api/rules/show", 200)
	saveTestEntryFull(t, hist, "GET", "sonarcloud.io", "/api/issues/search", 500)
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"sonarcloud.io"}, MatchMode: "all"}}
	sc := NewScope(hist, fs, nil, nil)

	page := sc.ListEntries(history.Filters{Path: []string{"/api/issues/"}, Status: []string{"200"}}, 0, 10)
	if len(page.Entries) != 1 {
		t.Fatalf("expected 1 sonarcloud.io /api/issues/ 200 entry, got %+v", page.Entries)
	}
	if page.Entries[0].Host != "sonarcloud.io" || page.Entries[0].Method != "GET" {
		t.Fatalf("wrong entry: %+v", page.Entries[0])
	}
	if page.VisibleCount != 1 {
		t.Errorf("visibleCount = %d, want 1", page.VisibleCount)
	}
}

func TestScope_QueryCannotWidenProfile(t *testing.T) {
	hist := newTestHistory(t)
	saveTestEntry(t, hist, "a.com", "", 200)
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"a.com"}, MatchMode: "all"}}
	sc := NewScope(hist, fs, nil, nil)

	page := sc.ListEntries(history.Filters{Host: []string{"github.com"}}, 0, 10)
	if len(page.Entries) != 0 || page.VisibleCount != 0 {
		t.Fatalf("query host outside the profile must yield nothing, got %+v", page)
	}
}

func TestScope_FilterValuesScoped(t *testing.T) {
	hist := newTestHistory(t)
	saveTestEntryFull(t, hist, "GET", "a.com", "/x", 200)
	saveTestEntryFull(t, hist, "POST", "b.com", "/y", 500)
	saveTestEntryFull(t, hist, "GET", "b.com", "/z", 200)
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"b.com"}, MatchMode: "all"}}
	sc := NewScope(hist, fs, nil, nil)

	hosts := sc.FilterValues("host")
	if len(hosts) != 1 || hosts[0].Value != "b.com" || hosts[0].Count != 2 {
		t.Fatalf("FilterValues(host) = %+v, want [b.com x2]", hosts)
	}
	methods := sc.FilterValues("method")
	if len(methods) != 2 || methods[0].Value != "GET" || methods[0].Count != 1 || methods[1].Value != "POST" || methods[1].Count != 1 {
		t.Fatalf("FilterValues(method) = %+v, want [GET x1, POST x1]", methods)
	}
}

func TestScope_FilterValuesGateOff(t *testing.T) {
	hist := newTestHistory(t)
	saveTestEntry(t, hist, "a.com", "", 200)
	sc := NewScope(hist, &mockFilterStore{gate: false}, nil, nil)
	if v := sc.FilterValues("host"); len(v) != 0 {
		t.Fatalf("FilterValues with gate off = %+v, want empty", v)
	}
}

func TestScope_SetHistoryStoreRotation(t *testing.T) {
	hist1 := newTestHistory(t)
	saveTestEntry(t, hist1, "a.com", "", 200)
	sc := NewScope(hist1, &mockFilterStore{gate: true}, nil, nil)

	if page := sc.ListEntries(history.Filters{}, 0, 10); page.VisibleCount != 1 {
		t.Fatalf("pre-rotation VisibleCount = %d, want 1", page.VisibleCount)
	}

	hist2 := newTestHistory(t)
	saveTestEntry(t, hist2, "b.com", "", 200)
	sc.SetHistoryStore(hist2)

	page := sc.ListEntries(history.Filters{}, 0, 10)
	if page.VisibleCount != 1 || len(page.Entries) != 1 {
		t.Fatalf("post-rotation page = %+v, want exactly the new store's entry", page)
	}
	if page.Entries[0].Host != "b.com" {
		t.Errorf("post-rotation entry host = %q, want b.com", page.Entries[0].Host)
	}
}

func TestScope_ReplayMode_GateBypass(t *testing.T) {
	hist := newTestHistory(t)
	a := saveTestEntry(t, hist, "a.com", "", 200)
	b := saveTestEntry(t, hist, "b.com", "", 200)

	// Gate off, no replay - nothing visible.
	fs := &mockFilterStore{gate: false}
	sc := NewScope(hist, fs, nil, nil)
	if page := sc.ListEntries(history.Filters{}, 0, 10); len(page.Entries) != 0 {
		t.Fatalf("gate off + no replay: expected 0 entries, got %d", len(page.Entries))
	}

	// Gate off + replay - all entries visible, no filter profile.
	sc.SetReplayMode(true)
	page := sc.ListEntries(history.Filters{}, 0, 10)
	if len(page.Entries) != 2 {
		t.Fatalf("replay mode: expected 2 entries, got %d", len(page.Entries))
	}
	if !sc.IsVisible(a.ID) || !sc.IsVisible(b.ID) {
		t.Error("replay mode: all entries must be visible")
	}
	if sc.GateEnabled() {
		t.Error("GateEnabled must still reflect the filter store (off), not replay mode")
	}
}

func TestScope_ReplayMode_NoFilterProfile(t *testing.T) {
	hist := newTestHistory(t)
	saveTestEntry(t, hist, "a.com", "", 200)
	saveTestEntry(t, hist, "b.com", "", 200)

	// Agent filter profile restricts to a.com, gate on.
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"a.com"}, MatchMode: "all"}}
	sc := NewScope(hist, fs, nil, nil)
	if page := sc.ListEntries(history.Filters{}, 0, 10); page.VisibleCount != 1 {
		t.Fatalf("record mode: expected 1 entry (filter profile), got %d", page.VisibleCount)
	}

	// Replay mode - filter profile ignored, all entries returned.
	sc.SetReplayMode(true)
	page := sc.ListEntries(history.Filters{}, 0, 10)
	if page.VisibleCount != 2 {
		t.Fatalf("replay mode: expected 2 entries (no filter profile), got %d", page.VisibleCount)
	}
}

func TestScope_ReplayMode_VisibleHosts(t *testing.T) {
	hist := newTestHistory(t)
	saveTestEntry(t, hist, "a.com", "", 200)
	saveTestEntry(t, hist, "b.com", "", 200)
	fs := &mockFilterStore{gate: false}
	sc := NewScope(hist, fs, nil, nil)

	// Gate off, no replay - empty.
	if hosts := sc.VisibleHosts(); len(hosts) != 0 {
		t.Fatalf("gate off: expected empty, got %v", hosts)
	}

	// Replay mode - all hosts.
	sc.SetReplayMode(true)
	hosts := sc.VisibleHosts()
	if len(hosts) != 2 {
		t.Fatalf("replay mode: expected 2 hosts, got %v", hosts)
	}
}

func TestScope_ReplayMode_FilterValues(t *testing.T) {
	hist := newTestHistory(t)
	saveTestEntryFull(t, hist, "GET", "a.com", "/x", 200)
	saveTestEntryFull(t, hist, "POST", "b.com", "/y", 500)
	fs := &mockFilterStore{gate: false}
	sc := NewScope(hist, fs, nil, nil)

	// Gate off, no replay - empty.
	if v := sc.FilterValues("host"); len(v) != 0 {
		t.Fatalf("gate off: expected empty, got %v", v)
	}

	// Replay mode - all values.
	sc.SetReplayMode(true)
	v := sc.FilterValues("host")
	if len(v) != 2 {
		t.Fatalf("replay mode: expected 2 hosts, got %v", v)
	}
}
