package webui

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"gospy/internal/history"
)

const (
	profileNormal = "normal"
	profileAgent  = "agent"
)

type profileState struct {
	Filters      history.Filters `json:"filters"`
	FocusEnabled bool            `json:"focusEnabled,omitempty"`

	// Body holds entry IDs produced by an on-demand deep search. It is volatile
	// session state for this profile - never written to disk.
	Body      []string `json:"-"`
	bodyToken uint64   `json:"-"`
}

type filterFile struct {
	Profiles     map[string]profileState `json:"profiles"`
	AgentPreview bool                    `json:"agentPreview"`

	// Legacy key (pre-agentPreview rename): migrated into AgentPreview on load.
	LegacyAgentEnabled *bool `json:"agentEnabled,omitempty"`

	// Legacy single-profile format (pre-agent-view): migrated into the "normal"
	// profile on load.
	Filters      history.Filters `json:"filters,omitempty"`
	FocusEnabled bool            `json:"focusEnabled,omitempty"`
}

// FilterStore holds the server-side filter criteria per view profile. The
// "normal" profile is what the WebUI edits with agent preview off; the "agent"
// profile defines the scope of the agent MCP. Both are persisted to
// filters.json; body search result IDs are volatile session state - never
// written to disk. The agent gate is in-memory session state - it resets to
// off on every start.
type FilterStore struct {
	path         string
	mu           sync.Mutex
	profiles     map[string]profileState
	agentPreview bool
	agentEnabled bool
	version      int
}

func NewFilterStore(path string) *FilterStore {
	return &FilterStore{
		path:     path,
		profiles: map[string]profileState{profileNormal: {}, profileAgent: {}},
		version:  1,
	}
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

	if len(f.Profiles) == 0 {
		// Legacy format: single profile becomes "normal".
		f.Profiles = map[string]profileState{
			profileNormal: {Filters: f.Filters, FocusEnabled: f.FocusEnabled},
			profileAgent:  {},
		}
	}
	for name, p := range f.Profiles {
		if name != profileNormal && name != profileAgent {
			continue
		}
		p.Body = nil
		f.Profiles[name] = p
	}
	s.profiles = f.Profiles
	s.agentPreview = f.AgentPreview
	if f.LegacyAgentEnabled != nil {
		s.agentPreview = *f.LegacyAgentEnabled
	}
	return nil
}

func (s *FilterStore) saveLocked() error {
	f := filterFile{
		Profiles:     make(map[string]profileState, len(s.profiles)),
		AgentPreview: s.agentPreview,
	}
	for name, p := range s.profiles {
		if name != profileNormal && name != profileAgent {
			continue
		}
		p.Body = nil
		f.Profiles[name] = p
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal filters: %w", err)
	}
	return os.WriteFile(s.path, data, 0644)
}

// ActiveProfile returns the profile currently edited/served by the WebUI,
// derived from the agent preview toggle.
func (s *FilterStore) ActiveProfile() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeProfileLocked()
}

func (s *FilterStore) activeProfileLocked() string {
	if s.agentPreview {
		return profileAgent
	}
	return profileNormal
}

// Snapshot returns the active profile's criteria, focus toggle, and the
// current version.
func (s *FilterStore) Snapshot() (history.Filters, bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.profiles[s.activeProfileLocked()]
	f := p.Filters
	f.Body = append([]string(nil), p.Body...)
	return f, p.FocusEnabled, s.version
}

// SnapshotAgent returns the agent profile's criteria and focus toggle - the
// scope served to the MCP.
func (s *FilterStore) SnapshotAgent() (history.Filters, bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.profiles[profileAgent]
	f := p.Filters
	f.Body = append([]string(nil), p.Body...)
	return f, p.FocusEnabled, s.version
}

// AgentPreview reports whether the WebUI previews the agent profile.
func (s *FilterStore) AgentPreview() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agentPreview
}

// AgentGate reports whether the agent MCP scope is active. The gate is
// in-memory session state - it resets to off on every start.
func (s *FilterStore) AgentGate() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agentEnabled
}

// SetAgentPreview toggles which profile the WebUI previews. Persists and
// bumps the version so consumers refetch. Returns the new version.
func (s *FilterStore) SetAgentPreview(preview bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentPreview = preview
	s.version++
	_ = s.saveLocked()
	return s.version
}

// SetAgentGate enables or disables the agent MCP scope. In-memory only - never
// persisted, so the gate is off on every fresh start. No version bump: the
// gate does not change the WebUI's visible list.
func (s *FilterStore) SetAgentGate(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentEnabled = enabled
}

// Version returns the current criteria version.
func (s *FilterStore) Version() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Set replaces the active profile's durable criteria and focus toggle. The
// caller's Body field is ignored - the current committed body IDs are
// preserved. Persists and bumps the version. Returns the new version.
func (s *FilterStore) Set(f history.Filters, focusEnabled bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := s.activeProfileLocked()
	p := s.profiles[name]
	f.Body = p.Body
	p.Filters = f
	p.FocusEnabled = focusEnabled
	s.profiles[name] = p
	s.version++
	_ = s.saveLocked()
	return s.version
}

// SetBodyIDs commits the deep search results for the given search session to
// the active profile. Never persisted; bumps the version. Returns the new
// version.
func (s *FilterStore) SetBodyIDs(ids []string, token uint64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setBodyIDsLocked(s.activeProfileLocked(), ids, token)
}

// SetBodyIDsFor commits deep search results to an explicit profile - the one
// the search started in, so a mid-scan view toggle cannot cross-contaminate.
func (s *FilterStore) SetBodyIDsFor(profile string, ids []string, token uint64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setBodyIDsLocked(profile, ids, token)
}

func (s *FilterStore) setBodyIDsLocked(profile string, ids []string, token uint64) int {
	p := s.profiles[profile]
	p.Body = ids
	p.bodyToken = token
	s.profiles[profile] = p
	s.version++
	return s.version
}

// ClearBody clears the active profile's committed body IDs only if they belong
// to the given search token (an aborted scan must not wipe a newer scan's
// results). Returns the new version.
func (s *FilterStore) ClearBody(token uint64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clearBodyLocked(s.activeProfileLocked(), token)
}

// ClearBodyFor clears an explicit profile's committed body IDs (used by the
// search abort path with the profile captured at search start).
func (s *FilterStore) ClearBodyFor(profile string, token uint64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clearBodyLocked(profile, token)
}

func (s *FilterStore) clearBodyLocked(profile string, token uint64) int {
	p := s.profiles[profile]
	if p.bodyToken != token {
		return s.version
	}
	p.Body = nil
	s.profiles[profile] = p
	s.version++
	return s.version
}

// ClearBodyAll unconditionally clears the active profile's committed body IDs
// (chip close). Returns the new version.
func (s *FilterStore) ClearBodyAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := s.activeProfileLocked()
	p := s.profiles[name]
	p.Body = nil
	s.profiles[name] = p
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
