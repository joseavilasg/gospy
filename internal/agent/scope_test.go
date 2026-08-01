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

	page := sc.ListEntries(0, 10)
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

	page := sc.ListEntries(0, 10)
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

	page := sc.ListEntries(0, 10)
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
	if page := sc.ListEntries(0, 10); len(page.Entries) != 0 {
		t.Errorf("ignored entry leaked into the page: %+v", page.Entries)
	}
}

func TestScope_Pagination(t *testing.T) {
	hist := newTestHistory(t)
	for i := 0; i < 3; i++ {
		saveTestEntry(t, hist, "a.com", "", 200)
	}
	sc := NewScope(hist, &mockFilterStore{gate: true}, nil, nil)

	first := sc.ListEntries(0, 2)
	if len(first.Entries) != 2 || !first.HasMore || first.VisibleCount != 3 {
		t.Fatalf("first page = %+v", first)
	}
	second := sc.ListEntries(2, 2)
	if len(second.Entries) != 1 || second.HasMore {
		t.Fatalf("second page = %+v", second)
	}
	// Offsets past the end return an empty, non-nil slice.
	past := sc.ListEntries(99, 2)
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
