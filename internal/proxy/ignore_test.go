package proxy

import (
	"path/filepath"
	"testing"
)

func TestIgnoreStore_Matches(t *testing.T) {
	dir := t.TempDir()
	store := NewIgnoreStore(filepath.Join(dir, "ignore.json"))
	store.Load()

	cases := []struct {
		name     string
		patterns []string
		host     string
		want     bool
	}{
		{"exact match", []string{"example.com"}, "example.com", true},
		{"exact no match", []string{"example.com"}, "other.com", false},
		{"wildcard subdomain", []string{"*.example.com"}, "api.example.com", true},
		{"wildcard subdomain no match", []string{"*.example.com"}, "example.com", false},
		{"wildcard prefix", []string{"telemetry.*"}, "telemetry.microsoft.com", true},
		{"wildcard prefix no match", []string{"telemetry.*"}, "logs.microsoft.com", false},
		{"multiple patterns", []string{"a.com", "*.b.com"}, "x.b.com", true},
		{"empty store", []string{}, "anything.com", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, p := range tc.patterns {
				store.Add(p)
			}
			got := store.Matches(tc.host)
			if got != tc.want {
				t.Errorf("Matches(%q) = %v, want %v", tc.host, got, tc.want)
			}
			for _, p := range tc.patterns {
				store.Remove(p)
			}
		})
	}
}

func TestIgnoreStore_IsIgnored(t *testing.T) {
	dir := t.TempDir()
	store := NewIgnoreStore(filepath.Join(dir, "ignore.json"))
	store.Load()

	store.Add("example.com")

	if !store.IsIgnored("example.com") {
		t.Error("IsIgnored(example.com) = false, want true")
	}
	if store.IsIgnored("other.com") {
		t.Error("IsIgnored(other.com) = true, want false")
	}
}

func TestIgnoreStore_AddRemove(t *testing.T) {
	dir := t.TempDir()
	store := NewIgnoreStore(filepath.Join(dir, "ignore.json"))
	store.Load()

	store.Add("a.com")
	store.Add("b.com")

	list := store.List()
	if len(list) != 2 {
		t.Errorf("List() = %d, want 2", len(list))
	}

	store.Remove("a.com")

	list = store.List()
	if len(list) != 1 {
		t.Errorf("List() after Remove = %d, want 1", len(list))
	}
	if list[0] != "b.com" {
		t.Errorf("List()[0] = %q, want %q", list[0], "b.com")
	}
}

func TestIgnoreStore_Dedup(t *testing.T) {
	dir := t.TempDir()
	store := NewIgnoreStore(filepath.Join(dir, "ignore.json"))
	store.Load()

	store.Add("example.com")
	store.Add("example.com")

	list := store.List()
	if len(list) != 1 {
		t.Errorf("List() dedup = %d, want 1", len(list))
	}
}

func TestIgnoreStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ignore.json")

	store1 := NewIgnoreStore(path)
	store1.Load()
	store1.Add("example.com")

	store2 := NewIgnoreStore(path)
	store2.Load()

	if !store2.IsIgnored("example.com") {
		t.Error("IsIgnored() after reload = false, want true")
	}
}
