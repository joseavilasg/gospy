// Package replay implements the replay candidate/diff analysis shared by the
// WebUI match tab and the agent MCP's list_replay_candidates / replay_diff
// tools. It is pure domain logic: given an incoming replay event, the recorded
// events of its run and the run's match config, it ranks and selects the
// recorded entries worth comparing against. It carries no HTTP or storage
// concerns; data is supplied through the EntrySource interface.
package replay

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"gospy/internal/history"
	"gospy/internal/session"
)

// EntrySource supplies the recorded-entries snapshot a candidate analysis
// needs, in the order the replay queue consumes them (recorded ascending,
// excluding entries with no recorded response). The WebUI server implements it
// over its history store.
type EntrySource interface {
	ListEntries() []*history.ListEntry
}

// Candidate is one recorded entry of the run. Tag reflects the entry's state
// at the event's time: served (the entry this HIT actually served), consumed
// (already served by an earlier event), pending (still unconsumed and
// matchable) or ignored (excluded by the match config's ignore rules, so it is
// never served). PotentialMatch reports whether the entry shares the event's
// host+path, i.e. it is a viable comparison candidate for this event
// regardless of its state. DiffCount ranks the pending potential matches and is
// only computed for them.
type Candidate struct {
	EntryID        string `json:"entryId"`
	Entry          int    `json:"entry"`
	Method         string `json:"method"`
	URL            string `json:"url"`
	Tag            string `json:"tag"`
	PotentialMatch bool   `json:"potentialMatch"`
	ConsumedBySeq  int    `json:"consumedBySeq,omitempty"`
	DiffCount      int    `json:"diffCount,omitempty"`
}

// CandidateFilter selects a sub-list of the candidate universe by per-entry
// attributes. Both fields are optional filters that combine with AND; a
// nil/empty value leaves its axis unrestricted. PotentialMatch pointers convey
// the difference between "unset" (nil) and an explicit true/false.
type CandidateFilter struct {
	PotentialMatch *bool  `json:"potentialMatch,omitempty"`
	Tag            string `json:"tag,omitempty"`
}

// ConsumedInfo describes an earlier hit event that consumed a recorded entry,
// used by the already-consumed warning.
type ConsumedInfo struct {
	EntryID       string `json:"entryId"`
	Entry         int    `json:"entry"`
	ConsumedBySeq int    `json:"consumedBySeq"`
	ConsumedAt    string `json:"consumedAt"`
}

// EntryRef is a recorded entry in queue order (recorded ascending, status !=
// 0), with its 1-based recorded ordinal.
type EntryRef struct {
	ID     string
	Method string
	URL    string
	Entry  int
}

// EntryRefs converts the recorded entries (in queue order) into entry refs,
// excluding entries with no recorded response.
func EntryRefs(entries []*history.ListEntry) []EntryRef {
	refs := make([]EntryRef, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		le := entries[i]
		if le.Status == nil || *le.Status == 0 {
			continue
		}
		refs = append(refs, EntryRef{
			ID:     le.ID,
			Method: le.Method,
			URL:    le.URL,
			Entry:  len(refs) + 1,
		})
	}
	return refs
}

// ConsumedBefore maps each recorded entry to the earliest hit event before
// thisSeq that served it.
func ConsumedBefore(events []session.ReplayEvent, thisSeq int) map[string]ConsumedInfo {
	m := make(map[string]ConsumedInfo)
	for i := range events {
		ev := &events[i]
		if ev.Result != "hit" || ev.EntryID == "" || ev.Seq >= thisSeq {
			continue
		}
		if _, ok := m[ev.EntryID]; ok {
			continue
		}
		m[ev.EntryID] = ConsumedInfo{
			EntryID:       ev.EntryID,
			ConsumedBySeq: ev.Seq,
			ConsumedAt:    ev.Timestamp.Format(time.RFC3339),
		}
	}
	return m
}

// EventBySeq returns the event with the given seq, or nil.
func EventBySeq(events []session.ReplayEvent, seq int) *session.ReplayEvent {
	for i := range events {
		if events[i].Seq == seq {
			return &events[i]
		}
	}
	return nil
}

// BuildCandidates builds the candidate universe for an event: every recorded
// entry of the run in queue order, each tagged with its state at the event's
// time (served / consumed / pending / ignored) and whether it is a potential
// match for this event (shares the incoming host+path). Consumers select
// sub-lists from the universe with SelectCandidates.
func BuildCandidates(ev *session.ReplayEvent, events []session.ReplayEvent, cfg *session.MatchConfig, src EntrySource) []Candidate {
	refs := EntryRefs(src.ListEntries())
	consumed := ConsumedBefore(events, ev.Seq)

	// Guarantee the universe is non-nil so an empty run serializes as
	// "entries":[], never null - a null entries blanks the whole match tab
	// on the frontend (it branches on the entries key).
	universe := make([]Candidate, 0, len(refs))

	incomingHP := session.HostPathKey(ev.URL, cfg)

	for _, r := range refs {
		c := Candidate{
			EntryID:        r.ID,
			Entry:          r.Entry,
			Method:         r.Method,
			URL:            r.URL,
			PotentialMatch: session.HostPathKey(r.URL, cfg) == incomingHP,
		}
		if r.ID == ev.EntryID && ev.Result == "hit" {
			c.Tag = "served"
		} else if ci, ok := consumed[r.ID]; ok {
			c.Tag = "consumed"
			c.ConsumedBySeq = ci.ConsumedBySeq
		} else if session.IsIgnored(r.URL, cfg) {
			c.Tag = "ignored"
		} else {
			c.Tag = "pending"
			if c.PotentialMatch {
				c.DiffCount = session.DiffURL(ev.URL, r.URL, cfg).DiffCount
			}
		}
		universe = append(universe, c)
	}

	return universe
}

// SelectCandidates filters the universe by CandidateFilter and returns the
// sub-list ordered for display: candidates selected as potential matches
// (PotentialMatch=true) are ranked served -> consumed -> pending by diff count
// ascending (the comparison order of the matching view); everything else keeps
// the universe's queue order.
func SelectCandidates(universe []Candidate, f CandidateFilter) []Candidate {
	out := make([]Candidate, 0, len(universe))
	for _, c := range universe {
		if f.PotentialMatch != nil && c.PotentialMatch != *f.PotentialMatch {
			continue
		}
		if f.Tag != "" && c.Tag != f.Tag {
			continue
		}
		out = append(out, c)
	}
	if f.PotentialMatch != nil && *f.PotentialMatch {
		rankMatches(out)
	}
	return out
}

// rankMatches orders potential-match candidates for comparison: the served
// entry first, then consumed-before entries, then pending entries ranked by
// diff count ascending, ties broken by recorded ordinal.
func rankMatches(cands []Candidate) {
	sort.SliceStable(cands, func(i, j int) bool {
		rank := func(c Candidate) int {
			switch c.Tag {
			case "served":
				return 0
			case "consumed":
				return 1
			default:
				return 2
			}
		}
		ri, rj := rank(cands[i]), rank(cands[j])
		if ri != rj {
			return ri < rj
		}
		if ri == 2 && cands[i].DiffCount != cands[j].DiffCount {
			return cands[i].DiffCount < cands[j].DiffCount
		}
		return cands[i].Entry < cands[j].Entry
	})
}

// Counts reports the size of each candidate view over the universe: the
// number of potential matches (matching), pending entries (pending) and
// entries ignored by the match config (ignored), independent of any active
// selection filter. The WebUI and MCP use it for their view headers so the
// totals stay stable under search.
func Counts(universe []Candidate) map[string]int {
	matching, pending, ignored := 0, 0, 0
	for _, c := range universe {
		if c.PotentialMatch {
			matching++
		}
		switch c.Tag {
		case "pending":
			pending++
		case "ignored":
			ignored++
		}
	}
	return map[string]int{"matching": matching, "pending": pending, "ignored": ignored}
}

// ConsumedExactCandidate returns the pool entry whose match key equals the
// incoming request exactly and was already consumed by an earlier event, or
// the zero candidate when there is none. It is the event-level diagnosis for a
// miss: the key that should have matched was taken by a prior request.
func ConsumedExactCandidate(ev *session.ReplayEvent, pool []Candidate, cfg *session.MatchConfig) Candidate {
	incomingKey := session.MatchKey(ev.Method, ev.URL, cfg)
	for _, c := range pool {
		if c.Tag == "consumed" && session.MatchKey(c.Method, c.URL, cfg) == incomingKey {
			return c
		}
	}
	return Candidate{}
}

// SelectCandidate picks the default candidate for an event: the served entry
// on a hit, else the consumed-before entry with the exact same match key (the
// already-consumed diagnosis: its key matches the incoming exactly but a
// prior request consumed it first, so the incoming was likely a duplicate or
// arrived out of order), else the best pending match by diff count, else the
// first row.
func SelectCandidate(ev *session.ReplayEvent, pool []Candidate, cfg *session.MatchConfig) Candidate {
	for _, c := range pool {
		if ev.Result == "hit" && c.Tag == "served" {
			return c
		}
	}
	if c := ConsumedExactCandidate(ev, pool, cfg); c.EntryID != "" {
		return c
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
	return Candidate{}
}

// FilterCandidates applies the match search query to a candidate pool.
// A pure numeric query matches the entry number exactly first; when no entry
// carries that number it falls back to the substring search over the visible
// surface, so "19" targets entry 19 while "5000" can still find media_5000.
func FilterCandidates(pool []Candidate, q string) []Candidate {
	if q == "" {
		return pool
	}
	q = strings.ToLower(q)
	if n, err := strconv.Atoi(q); err == nil {
		exact := make([]Candidate, 0, len(pool))
		for _, c := range pool {
			if c.Entry == n {
				exact = append(exact, c)
			}
		}
		if len(exact) > 0 {
			return exact
		}
	}
	out := make([]Candidate, 0, len(pool))
	for _, c := range pool {
		if strings.Contains(candidateSearchText(c), q) {
			out = append(out, c)
		}
	}
	return out
}

// searchURL returns the path+query of a recorded URL, the same fragment the
// match tab renders in its candidate rows (scheme and host are never shown), so
// the search surface matches what the user can actually see. Mirrors the
// frontend shortUrl() prefixing rules for scheme-less values.
func searchURL(raw string) string {
	candidate := raw
	if strings.HasPrefix(raw, "//") {
		candidate = "http:" + raw
	} else if !strings.Contains(raw, "://") {
		candidate = "http://" + raw
	}
	u, err := url.Parse(candidate)
	if err != nil {
		return raw
	}
	return u.RequestURI()
}

// candidateSearchText builds the searchable surface of a candidate - the
// "entry N" label, method, URL and the state tag as displayed in the match
// tab - so the query filter matches what the user sees in the list. Lowercased
// once so a single contains check covers every field.
func candidateSearchText(c Candidate) string {
	var b strings.Builder
	b.WriteString("entry ")
	b.WriteString(strconv.Itoa(c.Entry))
	b.WriteByte(' ')
	b.WriteString(c.Method)
	b.WriteByte(' ')
	b.WriteString(searchURL(c.URL))
	switch c.Tag {
	case "served":
		b.WriteString(" matched")
	case "consumed":
		b.WriteString(" consumed")
		if c.ConsumedBySeq > 0 {
			b.WriteString(" by seq ")
			b.WriteString(strconv.Itoa(c.ConsumedBySeq))
		}
	case "pending":
		b.WriteString(" pending")
		if c.DiffCount > 0 {
			b.WriteString(" diffs")
		} else {
			b.WriteString(" exact match")
		}
	}
	return strings.ToLower(b.String())
}
