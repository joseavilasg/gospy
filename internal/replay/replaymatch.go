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

// Candidate is one recorded entry in a candidate list. Tag reflects the
// entry's state at the event's time: served (the entry this HIT actually
// served), consumed (already served by an earlier event) or pending (still
// unconsumed). DiffCount is only computed in the matching scope, where it
// ranks the candidates.
type Candidate struct {
	EntryID       string `json:"entryId"`
	Entry         int    `json:"entry"`
	Method        string `json:"method"`
	URL           string `json:"url"`
	Tag           string `json:"tag"`
	ConsumedBySeq int    `json:"consumedBySeq,omitempty"`
	DiffCount     int    `json:"diffCount,omitempty"`
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

// BuildCandidates assembles the matching and all-pending candidate lists for
// an event: matching carries every entry sharing the incoming host+path,
// tagged with its state at the event's time (served / consumed / pending),
// while all-pending is strictly the unconsumed queue remaining after the
// event.
func BuildCandidates(ev *session.ReplayEvent, events []session.ReplayEvent, cfg *session.MatchConfig, src EntrySource) (matching, allPending []Candidate) {
	refs := EntryRefs(src.ListEntries())
	consumed := ConsumedBefore(events, ev.Seq)

	// Guarantee both lists are non-nil so empty scopes serialize as
	// "entries":[], never null - a null entries blanks the whole match tab
	// on the frontend (it branches on the entries key).
	matching = make([]Candidate, 0)
	allPending = make([]Candidate, 0)

	incomingHP := session.HostPathKey(ev.URL, cfg)

	for _, r := range refs {
		if session.HostPathKey(r.URL, cfg) != incomingHP {
			continue
		}
		c := Candidate{EntryID: r.ID, Entry: r.Entry, Method: r.Method, URL: r.URL}
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
		allPending = append(allPending, Candidate{EntryID: r.ID, Entry: r.Entry, Method: r.Method, URL: r.URL, Tag: "pending"})
	}

	return matching, allPending
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
