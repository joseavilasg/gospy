package webui

import (
	"os"
	"strings"
	"testing"

	"gospy/internal/history"
)

func TestFilterStore_LoadMissing(t *testing.T) {
	fs := NewFilterStore(t.TempDir() + "/filters.json")
	if err := fs.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	f, focus, _ := fs.Snapshot()
	if focus || len(f.Host) > 0 || f.MatchMode != "" {
		t.Fatalf("expected empty filters, got %+v focus=%v", f, focus)
	}
}

func TestFilterStore_SetPersists(t *testing.T) {
	path := t.TempDir() + "/filters.json"
	fs := NewFilterStore(path)
	f := history.Filters{
		Host:      []string{"api.example.com"},
		Referer:   []string{"github.com"},
		MatchMode: "any",
		Text:      "users",
	}
	fs.Set(f, true)

	// A fresh store (simulating restart) restores durable criteria but not body IDs.
	fs2 := NewFilterStore(path)
	if err := fs2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	g, focus, _ := fs2.Snapshot()
	if !focus {
		t.Fatal("expected focusEnabled restored")
	}
	if len(g.Host) != 1 || g.Host[0] != "api.example.com" {
		t.Errorf("host not restored: %+v", g.Host)
	}
	if g.MatchMode != "any" || g.Text != "users" {
		t.Errorf("matchMode/text not restored: %+v", g)
	}
	if len(g.Body) != 0 {
		t.Errorf("body must not be persisted: %+v", g.Body)
	}
}

func TestFilterStore_SetPreservesBody(t *testing.T) {
	fs := NewFilterStore(t.TempDir() + "/filters.json")
	fs.SetBodyIDs([]string{"e1", "e2"}, 1)
	fs.Set(history.Filters{Host: []string{"x.com"}}, false)

	f, _, _ := fs.Snapshot()
	if len(f.Body) != 2 || f.Body[0] != "e1" {
		t.Fatalf("Set must preserve committed body IDs: %+v", f.Body)
	}
}

func TestFilterStore_BodyTokenSemantics(t *testing.T) {
	fs := NewFilterStore(t.TempDir() + "/filters.json")

	// Search A commits, then aborts → cleared.
	fs.SetBodyIDs([]string{"a1", "a2"}, 1)
	if v := fs.ClearBody(1); v <= 0 {
		t.Fatal("ClearBody returned invalid version")
	}
	f, _, _ := fs.Snapshot()
	if len(f.Body) != 0 {
		t.Fatalf("expected body cleared after abort: %+v", f.Body)
	}

	// Search A aborts AFTER search B started → A must not wipe B.
	fs.SetBodyIDs([]string{"a1"}, 1)
	fs.SetBodyIDs([]string{"b1", "b2"}, 2)
	fs.ClearBody(1)
	f, _, _ = fs.Snapshot()
	if len(f.Body) != 2 || f.Body[0] != "b1" {
		t.Fatalf("stale abort wiped newer search results: %+v", f.Body)
	}

	// ClearBodyAll (chip close) is unconditional.
	fs.ClearBodyAll()
	f, _, _ = fs.Snapshot()
	if len(f.Body) != 0 {
		t.Fatalf("expected body cleared: %+v", f.Body)
	}
}

func TestFilterStore_VersionBumps(t *testing.T) {
	fs := NewFilterStore(t.TempDir() + "/filters.json")
	v1 := fs.Version()
	fs.SetBodyIDs([]string{"x"}, 1)
	v2 := fs.Version()
	fs.Touch()
	v3 := fs.Version()
	if !(v1 < v2 && v2 < v3) {
		t.Fatalf("expected strictly increasing versions, got %d %d %d", v1, v2, v3)
	}
}

func TestFilterStore_PersistedFileDoesNotIncludeBody(t *testing.T) {
	path := t.TempDir() + "/filters.json"
	fs := NewFilterStore(path)
	fs.Set(history.Filters{Host: []string{"x.com"}}, false)
	fs.SetBodyIDs([]string{"e1"}, 7)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	body := string(data)
	for _, sub := range []string{"e1", "\"body\"", "bodyToken"} {
		if strings.Contains(body, sub) {
			t.Errorf("persisted file must not contain %q: %s", sub, body)
		}
	}
}
