package agent

import (
	"strings"
	"testing"

	"gospy/internal/history"
)

// Tests for record auto mode before the first session start: the agent scope
// holds a nil store and must expose nothing without panicking.

func TestScopeNoSessionEmpty(t *testing.T) {
	sc := NewScope(nil, &mockFilterStore{gate: true}, nil, nil)
	page := sc.ListEntries(history.Filters{}, 0, 10)
	if len(page.Entries) != 0 || page.VisibleCount != 0 {
		t.Fatalf("no session must expose nothing, got %+v", page)
	}
	if sc.IsVisible("whatever") {
		t.Error("IsVisible must be false with no session")
	}
	if hosts := sc.VisibleHosts(); len(hosts) != 0 {
		t.Errorf("VisibleHosts with no session = %v", hosts)
	}
}

func TestMCPServerGetEntryNoSession(t *testing.T) {
	fwd, err := NewForwarder("http://127.0.0.1:9", emptyCert())
	if err != nil {
		t.Fatalf("NewForwarder: %v", err)
	}
	srv := NewServer(NewScope(nil, &mockFilterStore{gate: true}, nil, nil), nil, fwd)
	resp := callTool(t, srv.Handler(), "get_entry", map[string]any{"id": "whatever"})
	if !resp.Result.IsError {
		t.Fatalf("get_entry without session must error, got %+v", resp.Result)
	}
	if len(resp.Result.Content) == 0 || !strings.Contains(resp.Result.Content[0].Text, "no session recording active") {
		t.Errorf("error text = %+v, want 'no session recording active'", resp.Result.Content)
	}
}
