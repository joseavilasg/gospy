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

func TestFilterStore_MigratesLegacyFormat(t *testing.T) {
	path := t.TempDir() + "/filters.json"
	legacy := `{"filters":{"host":["api.example.com"],"matchMode":"any"},"focusEnabled":true}`
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	fs := NewFilterStore(path)
	if err := fs.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	f, focus, _ := fs.Snapshot()
	if !focus || len(f.Host) != 1 || f.Host[0] != "api.example.com" || f.MatchMode != "any" {
		t.Fatalf("legacy criteria not migrated into normal profile: %+v focus=%v", f, focus)
	}
	if fs.AgentPreview() {
		t.Fatal("agent preview must be off after legacy migration")
	}
	if fs.AgentGate() {
		t.Fatal("agent gate must be off after legacy migration")
	}

	// A subsequent save rewrites the file in the new two-profile format.
	fs.Set(history.Filters{Host: []string{"other.com"}}, false)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !strings.Contains(string(data), "\"profiles\"") || !strings.Contains(string(data), "\"agentPreview\"") {
		t.Fatalf("file not rewritten in two-profile format: %s", data)
	}
}

func TestFilterStore_MigratesLegacyAgentEnabledKey(t *testing.T) {
	path := t.TempDir() + "/filters.json"
	legacy := `{"profiles":{"normal":{"filters":{"host":["api.example.com"]}},"agent":{"filters":{"origin":["agent"]}}},"agentEnabled":true}`
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	fs := NewFilterStore(path)
	if err := fs.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !fs.AgentPreview() {
		t.Fatal("agentPreview must migrate from the legacy agentEnabled key")
	}
	if fs.AgentGate() {
		t.Fatal("the gate must stay off on load")
	}
	f, _, _ := fs.Snapshot()
	if len(f.Origin) != 1 || f.Origin[0] != "agent" {
		t.Fatalf("agent profile not loaded: %+v", f)
	}
}

func TestFilterStore_AgentViewTogglePersists(t *testing.T) {
	path := t.TempDir() + "/filters.json"
	fs := NewFilterStore(path)

	fs.Set(history.Filters{Host: []string{"api.example.com"}, MatchMode: "all"}, true)
	fs.SetAgentPreview(true)
	fs.Set(history.Filters{Origin: []string{"agent"}, MatchMode: "any"}, false)

	// Fresh store (restart) restores the agent profile and the preview.
	fs2 := NewFilterStore(path)
	if err := fs2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !fs2.AgentPreview() {
		t.Fatal("agentPreview not restored")
	}
	f, focus, _ := fs2.Snapshot()
	if len(f.Origin) != 1 || f.Origin[0] != "agent" || f.MatchMode != "any" || focus {
		t.Fatalf("agent profile criteria not restored: %+v focus=%v", f, focus)
	}
	// Normal profile survives intact.
	fs2.SetAgentPreview(false)
	fn, focusN, _ := fs2.Snapshot()
	if len(fn.Host) != 1 || fn.Host[0] != "api.example.com" || !focusN {
		t.Fatalf("normal profile clobbered: %+v focus=%v", fn, focusN)
	}

	af, _, _ := fs2.SnapshotAgent()
	if fs2.AgentPreview() {
		t.Fatal("agent preview must be off after toggle off")
	}
	if len(af.Origin) != 1 || af.Origin[0] != "agent" {
		t.Fatalf("agent criteria must survive toggle: %+v", af)
	}
}

func TestFilterStore_PerProfileBody(t *testing.T) {
	fs := NewFilterStore(t.TempDir() + "/filters.json")

	fs.SetBodyIDs([]string{"n1", "n2"}, 1)
	fs.SetAgentPreview(true)
	fs.SetBodyIDs([]string{"a1"}, 2)

	f, _, _ := fs.Snapshot()
	if len(f.Body) != 1 || f.Body[0] != "a1" {
		t.Fatalf("active profile (agent) body: %+v", f.Body)
	}
	// Toggling back restores the normal profile's committed body IDs.
	fs.SetAgentPreview(false)
	fn, _, _ := fs.Snapshot()
	if len(fn.Body) != 2 || fn.Body[0] != "n1" {
		t.Fatalf("normal profile body lost after toggle: %+v", fn.Body)
	}
	// Chip close on the active profile must not touch the other profile.
	fs.ClearBodyAll()
	fn, _, _ = fs.Snapshot()
	if len(fn.Body) != 0 {
		t.Fatalf("ClearBodyAll failed: %+v", fn.Body)
	}
	af, _, _ := fs.SnapshotAgent()
	if len(af.Body) != 1 {
		t.Fatalf("agent body cleared by normal-profile ClearBodyAll: %+v", af.Body)
	}
}

func TestFilterStore_SearchTargetsCapturedProfile(t *testing.T) {
	fs := NewFilterStore(t.TempDir() + "/filters.json")

	// Search starts in the normal profile, then the user toggles mid-scan.
	fs.SetBodyIDsFor(profileNormal, []string{"x1", "x2"}, 5)
	fs.SetAgentPreview(true)
	fs.SetBodyIDs([]string{"a1"}, 6)

	// The abort must clear the profile the search started in, not the new active one.
	fs.ClearBodyFor(profileNormal, 5)
	fn, _, _ := fs.SnapshotAgent()
	if len(fn.Body) != 1 || fn.Body[0] != "a1" {
		t.Fatalf("agent body clobbered by normal-profile abort: %+v", fn.Body)
	}
	fs.SetAgentPreview(false)
	f, _, _ := fs.Snapshot()
	if len(f.Body) != 0 {
		t.Fatalf("normal body not cleared by its own abort: %+v", f.Body)
	}
}

func TestFilterStore_SetAgentPreviewBumpsVersion(t *testing.T) {
	fs := NewFilterStore(t.TempDir() + "/filters.json")
	v1 := fs.Version()
	fs.SetAgentPreview(true)
	v2 := fs.Version()
	if !(v2 > v1) {
		t.Fatalf("toggle must bump version: %d → %d", v1, v2)
	}
	if !fs.AgentPreview() {
		t.Fatal("agent preview not on after toggle")
	}
}

func TestFilterStore_AgentGateIsSessionState(t *testing.T) {
	path := t.TempDir() + "/filters.json"
	fs := NewFilterStore(path)
	if fs.AgentGate() {
		t.Fatal("gate must default to off")
	}

	fs.Set(history.Filters{Host: []string{"x.com"}}, false)
	fs.SetAgentGate(true)
	if !fs.AgentGate() {
		t.Fatal("gate must be on after SetAgentGate(true)")
	}

	// A fresh store (restart) must start with the gate OFF: it is never
	// persisted, only the preview and profile criteria are.
	fs2 := NewFilterStore(path)
	if err := fs2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if fs2.AgentGate() {
		t.Fatal("gate must reset to off on restart")
	}
	f2, _, _ := fs2.Snapshot()
	if len(f2.Host) != 1 {
		t.Fatal("profile criteria must still persist across restart")
	}
}

func TestFilterStore_AgentGateIndependentOfPreview(t *testing.T) {
	fs := NewFilterStore(t.TempDir() + "/filters.json")

	fs.SetAgentGate(true)
	if fs.AgentPreview() {
		t.Fatal("gate must not flip the preview")
	}
	fs.SetAgentPreview(true)
	if !fs.AgentGate() {
		t.Fatal("preview must not clear the gate")
	}
	fs.SetAgentGate(false)
	if !fs.AgentPreview() {
		t.Fatal("clearing the gate must not flip the preview")
	}
}
