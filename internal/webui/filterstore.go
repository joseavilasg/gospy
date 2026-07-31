package webui

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"gospy/internal/history"
)

type filterFile struct {
	Filters      history.Filters `json:"filters"`
	FocusEnabled bool            `json:"focusEnabled,omitempty"`
}

// FilterStore holds the single server-side filter criteria. Durable fields
// (hosts, referers, processes, content types, text, match mode, focus toggle)
// are persisted to filters.json; the body search result IDs are volatile
// session state - never written to disk.
type FilterStore struct {
	path         string
	mu           sync.Mutex
	filters      history.Filters
	focusEnabled bool
	version      int
	bodyToken    uint64
}

func NewFilterStore(path string) *FilterStore {
	return &FilterStore{path: path, version: 1}
}

func (s *FilterStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read filters file: %w", err)
	}

	var f filterFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("unmarshal filters file: %w", err)
	}
	f.Filters.Body = nil
	s.filters = f.Filters
	s.focusEnabled = f.FocusEnabled
	return nil
}

func (s *FilterStore) saveLocked() error {
	f := filterFile{Filters: s.filters, FocusEnabled: s.focusEnabled}
	f.Filters.Body = nil
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal filters: %w", err)
	}
	return os.WriteFile(s.path, data, 0644)
}

// Snapshot returns a copy of the criteria, the focus toggle, and the current version.
func (s *FilterStore) Snapshot() (history.Filters, bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.filters
	f.Body = append([]string(nil), s.filters.Body...)
	return f, s.focusEnabled, s.version
}

// Version returns the current criteria version.
func (s *FilterStore) Version() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Set replaces the durable criteria and focus toggle. The caller's Body field is
// ignored - the current committed body IDs are preserved. Persists and bumps the
// version. Returns the new version.
func (s *FilterStore) Set(f history.Filters, focusEnabled bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	f.Body = s.filters.Body
	s.filters = f
	s.focusEnabled = focusEnabled
	s.version++
	_ = s.saveLocked()
	return s.version
}

// SetBodyIDs commits the deep search results for the given search session.
// Never persisted; bumps the version so consumers refetch. Returns the new version.
func (s *FilterStore) SetBodyIDs(ids []string, token uint64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filters.Body = ids
	s.bodyToken = token
	s.version++
	return s.version
}

// ClearBody clears the committed body IDs only if they belong to the given search
// token (an aborted scan must not wipe a newer scan's results). Returns the new version.
func (s *FilterStore) ClearBody(token uint64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bodyToken != token {
		return s.version
	}
	s.filters.Body = nil
	s.version++
	return s.version
}

// ClearBodyAll unconditionally clears the committed body IDs (chip close).
// Returns the new version.
func (s *FilterStore) ClearBodyAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filters.Body = nil
	s.version++
	return s.version
}

// Touch bumps the version without changing criteria - used when an ignore or
// focus list change invalidates the visible-set cache. Returns the new version.
func (s *FilterStore) Touch() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	return s.version
}
