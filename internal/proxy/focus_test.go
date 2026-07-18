package proxy

import (
	"path/filepath"
	"testing"
)

func TestFocusStore_Matches(t *testing.T) {
	dir := t.TempDir()
	store := NewFocusStore(filepath.Join(dir, "focus.json"))
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

func TestFocusStore_IsFocused(t *testing.T) {
	dir := t.TempDir()
	store := NewFocusStore(filepath.Join(dir, "focus.json"))
	store.Load()

	store.Add("example.com")

	if !store.IsFocused("example.com") {
		t.Error("IsFocused(example.com) = false, want true")
	}
	if store.IsFocused("other.com") {
		t.Error("IsFocused(other.com) = true, want false")
	}
}

func TestFocusStore_EmptyIsFocused(t *testing.T) {
	dir := t.TempDir()
	store := NewFocusStore(filepath.Join(dir, "focus.json"))
	store.Load()

	if store.IsFocused("anything.com") {
		t.Error("IsFocused() empty store = true, want false")
	}
}

func TestFocusStore_AddRemove(t *testing.T) {
	dir := t.TempDir()
	store := NewFocusStore(filepath.Join(dir, "focus.json"))
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

func TestFocusStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "focus.json")

	store1 := NewFocusStore(path)
	store1.Load()
	store1.Add("example.com")

	store2 := NewFocusStore(path)
	store2.Load()

	if !store2.IsFocused("example.com") {
		t.Error("IsFocused() after reload = false, want true")
	}
}
