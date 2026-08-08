package webui

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"gospy/internal/session"
)

// replayCandidate is one recorded entry in the match tab's candidate list.
// Tag reflects the entry's state at the event's time: served (the entry this
// HIT actually served), consumed (already served by an earlier event) or
// pending (still unconsumed). DiffCount is only computed in the matching
// scope, where it ranks the candidates.
type replayCandidate struct {
	EntryID       string `json:"entryId"`
	Entry         int    `json:"entry"`
	Method        string `json:"method"`
	URL           string `json:"url"`
	Tag           string `json:"tag"`
	ConsumedBySeq int    `json:"consumedBySeq,omitempty"`
	DiffCount     int    `json:"diffCount,omitempty"`
}

type replayConsumedInfo struct {
	EntryID       string `json:"entryId"`
	Entry         int    `json:"entry"`
	ConsumedBySeq int    `json:"consumedBySeq"`
	ConsumedAt    string `json:"consumedAt"`
}

type replayCandidatesResponse struct {
	Scope       string               `json:"scope"`
	Total       map[string]int       `json:"total"`
	Entries     []replayCandidate    `json:"entries"`
	SelectedID  string               `json:"selectedEntryId,omitempty"`
	Diff        *session.DiffResult  `json:"diff,omitempty"`
	Consumed    *replayConsumedInfo  `json:"consumed,omitempty"`
	MatchConfig *session.MatchConfig `json:"matchConfig,omitempty"`
}

type replayCandidateDiffResponse struct {
	Entry replayCandidate     `json:"entry"`
	Diff  *session.DiffResult `json:"diff"`
}

// entryRef is a recorded entry in queue order (recorded ascending, status !=
// 0), with its 1-based recorded ordinal.
type entryRef struct {
	ID     string
	Method string
	URL    string
	Entry  int
}

// sessionEntryRefs lists the recorded entries in the order the replay queue
// consumes them, excluding entries with no recorded response.
func (s *Server) sessionEntryRefs() []entryRef {
	all := s.listSummary()
	refs := make([]entryRef, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		le := all[i]
		if le.Status == nil || *le.Status == 0 {
			continue
		}
		refs = append(refs, entryRef{
			ID:     le.ID,
			Method: le.Method,
			URL:    le.URL,
			Entry:  len(refs) + 1,
		})
	}
	return refs
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

// consumedBefore maps each recorded entry to the earliest hit event before
// thisSeq that served it.
func consumedBefore(events []session.ReplayEvent, thisSeq int) map[string]replayConsumedInfo {
	m := make(map[string]replayConsumedInfo)
	for i := range events {
		ev := &events[i]
		if ev.Result != "hit" || ev.EntryID == "" || ev.Seq >= thisSeq {
			continue
		}
		if _, ok := m[ev.EntryID]; ok {
			continue
		}
		m[ev.EntryID] = replayConsumedInfo{
			EntryID:       ev.EntryID,
			ConsumedBySeq: ev.Seq,
			ConsumedAt:    ev.Timestamp.Format(time.RFC3339),
		}
	}
	return m
}

// replayEventBySeq returns the event with the given seq, or nil.
func replayEventBySeq(events []session.ReplayEvent, seq int) *session.ReplayEvent {
	for i := range events {
		if events[i].Seq == seq {
			return &events[i]
		}
	}
	return nil
}

// buildReplayCandidates assembles the matching and all-pending candidate lists
// for an event: matching carries every entry sharing the incoming host+path,
// tagged with its state at the event's time (served / consumed / pending),
// while all-pending is strictly the unconsumed queue remaining after the
// event.
func (s *Server) buildReplayCandidates(ev *session.ReplayEvent, events []session.ReplayEvent, cfg *session.MatchConfig) (matching, allPending []replayCandidate) {
	refs := s.sessionEntryRefs()
	consumed := consumedBefore(events, ev.Seq)

	incomingHP := session.HostPathKey(ev.URL, cfg)

	for _, r := range refs {
		if session.HostPathKey(r.URL, cfg) != incomingHP {
			continue
		}
		c := replayCandidate{EntryID: r.ID, Entry: r.Entry, Method: r.Method, URL: r.URL}
		if r.ID == ev.EntryID && ev.Result == "hit" {
			c.Tag = "served"
		} else if ci, ok := consumed[r.ID]; ok {
			c.Tag = "consumed"
			c.ConsumedBySeq = ci.ConsumedBySeq
		} else {
			c.Tag = "pending"
			c.DiffCount = session.DiffURL(ev.URL, r.URL, cfg).DiffCount
		}
		matching = append(matching, c)
	}

	// Order the matching list: the served entry first, then consumed-before
	// entries, then pending entries ranked by diff count ascending.
	sort.SliceStable(matching, func(i, j int) bool {
		rank := func(c replayCandidate) int {
			switch c.Tag {
			case "served":
				return 0
			case "consumed":
				return 1
			default:
				return 2
			}
		}
		ri, rj := rank(matching[i]), rank(matching[j])
		if ri != rj {
			return ri < rj
		}
		if ri == 2 && matching[i].DiffCount != matching[j].DiffCount {
			return matching[i].DiffCount < matching[j].DiffCount
		}
		return matching[i].Entry < matching[j].Entry
	})

	// The pending set remaining after this event: the miss snapshot embeds it;
	// for hits it is reconstructed as everything not yet consumed, minus the
	// entry this hit just served (it is gone after the event). Every candidate
	// in this set is pending by construction.
	pendingIDs := make(map[string]bool)
	if ev.Result == "miss" && len(ev.Unconsumed) > 0 {
		for _, u := range ev.Unconsumed {
			pendingIDs[u.ID] = true
		}
	} else {
		for _, r := range refs {
			if ev.Result == "hit" && r.ID == ev.EntryID {
				continue
			}
			if _, ok := consumed[r.ID]; !ok {
				pendingIDs[r.ID] = true
			}
		}
	}

	for _, r := range refs {
		if !pendingIDs[r.ID] {
			continue
		}
		allPending = append(allPending, replayCandidate{EntryID: r.ID, Entry: r.Entry, Method: r.Method, URL: r.URL, Tag: "pending"})
	}

	return matching, allPending
}

// selectReplayCandidate picks the default candidate for an event: the served
// entry on a hit, else the consumed-before entry with the exact same match key
// (the already-consumed diagnosis: its key matches the incoming exactly but a
// prior request consumed it first, so the incoming was likely a duplicate or
// arrived out of order), else the best pending match by diff count, else the
// first row.
func selectReplayCandidate(ev *session.ReplayEvent, pool []replayCandidate, cfg *session.MatchConfig) replayCandidate {
	for _, c := range pool {
		if ev.Result == "hit" && c.Tag == "served" {
			return c
		}
	}
	incomingKey := session.MatchKey(ev.Method, ev.URL, cfg)
	for _, c := range pool {
		if c.Tag == "consumed" && session.MatchKey(c.Method, c.URL, cfg) == incomingKey {
			return c
		}
	}
	best := -1
	bestDiff := 1 << 30
	for i, c := range pool {
		if c.Tag != "pending" {
			continue
		}
		if best < 0 || c.DiffCount < bestDiff {
			best = i
			bestDiff = c.DiffCount
		}
	}
	if best >= 0 {
		return pool[best]
	}
	if len(pool) > 0 {
		return pool[0]
	}
	return replayCandidate{}
}

// handleReplayCandidates serves the unified candidate list of a replay event:
// scope=matching (entries sharing host+path, diff-ranked) or scope=all (the
// pending queue remaining after the event), with an optional q substring filter. The
// response carries the default selection, its diff and the consumed info that
// drives the already-consumed warning when the selected entry was consumed by
// an earlier event.
func (s *Server) handleReplayCandidates(w http.ResponseWriter, r *http.Request, runID string, seq int) {
	events, err := s.replayEventsFor(runID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ev := replayEventBySeq(events, seq)
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
	matching, allPending := s.buildReplayCandidates(ev, events, cfg)
	consumed := consumedBefore(events, seq)

	pool := matching
	if scope == "all" {
		pool = allPending
	}
	if q != "" {
		filtered := make([]replayCandidate, 0, len(pool))
		for _, c := range pool {
			if strings.Contains(strings.ToLower(c.URL), q) ||
				strings.Contains(strings.ToLower(c.Method), q) ||
				strings.Contains(strconv.Itoa(c.Entry), q) {
				filtered = append(filtered, c)
			}
		}
		pool = filtered
	}

	selected := selectReplayCandidate(ev, pool, cfg)

	resp := replayCandidatesResponse{
		Scope:   scope,
		Total:   map[string]int{"matching": len(matching), "pending": len(allPending)},
		Entries: pool,
	}
	if selected.EntryID != "" {
		resp.SelectedID = selected.EntryID
		resp.Diff = session.DiffURL(ev.URL, selected.URL, cfg)
		if ci, ok := consumed[selected.EntryID]; ok {
			resp.Consumed = &replayConsumedInfo{
				EntryID:       ci.EntryID,
				Entry:         selected.Entry,
				ConsumedBySeq: ci.ConsumedBySeq,
				ConsumedAt:    ci.ConsumedAt,
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
	ev := replayEventBySeq(events, seq)
	if ev == nil {
		http.NotFound(w, r)
		return
	}
	cfg := s.runMatchConfig(ev.RunID)
	var c replayCandidate
	found := false
	for _, ref := range s.sessionEntryRefs() {
		if ref.ID == entryID {
			c = replayCandidate{EntryID: ref.ID, Entry: ref.Entry, Method: ref.Method, URL: ref.URL}
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
