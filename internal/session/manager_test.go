package session

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"gospy/internal/history"
)

func TestManagerStartNamed(t *testing.T) {
	base := t.TempDir()
	m := NewManager(base)

	dir, store, err := m.Start("mysession")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if dir != filepath.Join(base, "mysession") {
		t.Errorf("dir = %q, want %q", dir, filepath.Join(base, "mysession"))
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("session dir missing: %v", err)
	}

	e := &history.Entry{ID: "e1"}
	if err := store.Save(e); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := store.ListSummary(); len(got) != 1 {
		t.Errorf("store ListSummary = %d entries, want 1", len(got))
	}
}

func TestManagerStartAutoName(t *testing.T) {
	base := t.TempDir()
	m := NewManager(base)

	dir, _, err := m.Start("")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !regexp.MustCompile(`^\d{8}-\d{6}$`).MatchString(filepath.Base(dir)) {
		t.Errorf("auto name = %q, want 20060102-150405 pattern", filepath.Base(dir))
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("session dir missing: %v", err)
	}
}

func TestManagerStartCollision(t *testing.T) {
	base := t.TempDir()
	m := NewManager(base)

	_, _, err := m.Start("same")
	if err != nil {
		t.Fatalf("Start 1: %v", err)
	}
	dir2, _, err := m.Start("same")
	if err != nil {
		t.Fatalf("Start 2: %v", err)
	}
	dir3, _, err := m.Start("same")
	if err != nil {
		t.Fatalf("Start 3: %v", err)
	}
	if dir2 != filepath.Join(base, "same-2") {
		t.Errorf("dir2 = %q, want %q", dir2, filepath.Join(base, "same-2"))
	}
	if dir3 != filepath.Join(base, "same-3") {
		t.Errorf("dir3 = %q, want %q", dir3, filepath.Join(base, "same-3"))
	}
}

func TestManagerStartInvalidName(t *testing.T) {
	base := t.TempDir()
	m := NewManager(base)

	for _, name := range []string{"..", "../evil", "a/b", `a\b`, ".hidden", "/abs"} {
		if _, _, err := m.Start(name); err == nil {
			t.Errorf("Start(%q): expected error, got nil", name)
		}
	}
}
