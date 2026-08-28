package webui

import (
	"net/http"
	"strings"

	"gospy/internal/history"
	"gospy/internal/replay"
	"gospy/internal/session"
)

// replayCandidatesResponse is the HTTP response for the match tab candidate
// list. Entries, the selected diff and the consumed warning are domain data
// produced by internal/replay.
type replayCandidatesResponse struct {
	Scope       string               `json:"scope"`
	Total       map[string]int       `json:"total"`
	Entries     []replay.Candidate   `json:"entries"`
	SelectedID  string               `json:"selectedEntryId,omitempty"`
	Diff        *session.DiffResult  `json:"diff,omitempty"`
	Consumed    *replay.ConsumedInfo `json:"consumed,omitempty"`
	MatchConfig *session.MatchConfig `json:"matchConfig,omitempty"`
}

type replayCandidateDiffResponse struct {
	Entry replay.Candidate    `json:"entry"`
	Diff  *session.DiffResult `json:"diff"`
}

// ListEntries supplies the recorded-entries snapshot to the replay analysis.
// Implements replay.EntrySource over the server's history store.
func (s *Server) ListEntries() []*history.ListEntry {
	return s.listSummary()
}

// runMatchConfig loads the match config a run was served under, so past runs
// are diffed with the exact config that drove their matches.
func (s *Server) runMatchConfig(runID string) *session.MatchConfig {
	dir, err := session.ReplayRunDir(s.replayLogDir, runID)
	if err != nil {
		return &session.MatchConfig{}
	}
	cfg, err := session.ReadMatchConfig(dir)
	if err != nil {
		return &session.MatchConfig{}
	}
	return cfg
}

// handleReplayCandidates serves the unified candidate list of a replay event:
// scope=matching (entries sharing host+path, diff-ranked) or scope=all (the
// pending queue remaining after the event), with an optional q substring filter. The
// response carries the default selection, its diff, and - only when the event
// itself is a miss whose exact match key was already consumed - the consumed
// info that drives the already-consumed warning (an event property, not tied
// to the selected row).
func (s *Server) handleReplayCandidates(w http.ResponseWriter, r *http.Request, runID string, seq int) {
	events, err := s.replayEventsFor(runID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ev := replay.EventBySeq(events, seq)
	if ev == nil {
		http.NotFound(w, r)
		return
	}

	scope := r.URL.Query().Get("scope")
	if scope != "all" {
		scope = "matching"
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	cfg := s.runMatchConfig(ev.RunID)
	matching, allPending := replay.BuildCandidates(ev, events, cfg, s)
	consumed := replay.ConsumedBefore(events, seq)

	pool := matching
	if scope == "all" {
		pool = allPending
	}
	if q != "" {
		pool = replay.FilterCandidates(pool, q)
	}

	selected := replay.SelectCandidate(ev, pool, cfg)

	resp := replayCandidatesResponse{
		Scope:   scope,
		Total:   map[string]int{"matching": len(matching), "pending": len(allPending)},
		Entries: pool,
	}
	if selected.EntryID != "" {
		resp.SelectedID = selected.EntryID
		resp.Diff = session.DiffURL(ev.URL, selected.URL, cfg)
	}
	// The already-consumed warning diagnoses the event, not the selection: it
	// appears only when a miss failed because its exact match key was consumed
	// by an earlier request. Hits never carry it, no matter which candidate is
	// inspected, and its presence never changes with the selected row.
	if ev.Result == "miss" {
		if c := replay.ConsumedExactCandidate(ev, matching, cfg); c.EntryID != "" {
			if ci, ok := consumed[c.EntryID]; ok {
				resp.Consumed = &replay.ConsumedInfo{
					EntryID:       ci.EntryID,
					Entry:         c.Entry,
					ConsumedBySeq: ci.ConsumedBySeq,
					ConsumedAt:    ci.ConsumedAt,
				}
			}
		}
	}
	resp.MatchConfig = cfg
	s.writeJSON(w, resp)
}

// handleReplayCandidateDiff serves the diff of one recorded entry against the
// event's incoming URL, on demand when the user changes the selection.
func (s *Server) handleReplayCandidateDiff(w http.ResponseWriter, r *http.Request, runID string, seq int, entryID string) {
	events, err := s.replayEventsFor(runID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ev := replay.EventBySeq(events, seq)
	if ev == nil {
		http.NotFound(w, r)
		return
	}
	cfg := s.runMatchConfig(ev.RunID)
	var c replay.Candidate
	found := false
	for _, ref := range replay.EntryRefs(s.ListEntries()) {
		if ref.ID == entryID {
			c = replay.Candidate{EntryID: ref.ID, Entry: ref.Entry, Method: ref.Method, URL: ref.URL}
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	s.writeJSON(w, replayCandidateDiffResponse{
		Entry: c,
		Diff:  session.DiffURL(ev.URL, c.URL, cfg),
	})
}
