package replay

import (
	"encoding/json"
	"testing"

	"gospy/internal/history"
	"gospy/internal/session"
)

// mustConfig builds a MatchConfig from its JSON form (the only way to
// construct session.Match patterns from outside the session package).
func mustConfig(t *testing.T, raw string) *session.MatchConfig {
	t.Helper()
	var cfg session.MatchConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("parse match config: %v", err)
	}
	return &cfg
}

// TestFilterCandidates covers the match search predicate: a numeric query
// matches the entry number exactly (so "19" targets entry 19 and not the HLS
// segment timestamps that also contain "19"), falling back to the visible
// substring surface when no entry carries that number, and the surface never
// includes the scheme or host the candidate rows do not render.
func TestFilterCandidates(t *testing.T) {
	seg19 := Candidate{EntryID: "e24", Entry: 24, Method: "GET", URL: "https://cdn.mdstrm.com/live/media_5000_20260804T211902_779386.ts", Tag: "pending"}
	seg29 := Candidate{EntryID: "e28", Entry: 28, Method: "GET", URL: "https://cdn.mdstrm.com/live/media_5000_20260804T212938_779411.ts", Tag: "pending"}
	entry19 := Candidate{EntryID: "e19", Entry: 19, Method: "GET", URL: "https://cdn.mdstrm.com/live/media_5000.m3u8", Tag: "pending"}
	consumed := Candidate{EntryID: "e2", Entry: 2, Method: "GET", URL: "https://cdn.mdstrm.com/live/media_5000.m3u8", Tag: "consumed", ConsumedBySeq: 7}
	pool := []Candidate{entry19, seg19, seg29, consumed}

	t.Run("numeric query matches the entry number exactly", func(t *testing.T) {
		got := FilterCandidates(pool, "19")
		if len(got) != 1 || got[0].Entry != 19 {
			t.Fatalf("q=19: want only entry 19, got %+v", got)
		}
	})

	t.Run("numeric query falls back to the URL surface without an entry hit", func(t *testing.T) {
		got := FilterCandidates(pool, "5000")
		if len(got) != 4 {
			t.Fatalf("q=5000: no entry 5000, must fall back to the media_5000 URL matches (all 4), got %d: %+v", len(got), got)
		}
	})

	t.Run("host and scheme are not part of the search surface", func(t *testing.T) {
		if got := FilterCandidates(pool, "mdstrm"); len(got) != 0 {
			t.Fatalf("q=mdstrm: the host must never match the visible-surface search, got %+v", got)
		}
		if got := FilterCandidates(pool, "https"); len(got) != 0 {
			t.Fatalf("q=https: the scheme must never match the visible-surface search, got %+v", got)
		}
	})

	t.Run("URL path fragments remain searchable", func(t *testing.T) {
		got := FilterCandidates(pool, "T2119")
		if len(got) != 1 || got[0].Entry != 24 {
			t.Fatalf("q=T2119: want only the segment carrying that timestamp, got %+v", got)
		}
	})

	t.Run("tag text is searchable", func(t *testing.T) {
		got := FilterCandidates(pool, "consumed by seq 7")
		if len(got) != 1 || got[0].Entry != 2 {
			t.Fatalf("q=consumed by seq 7: want the consumed entry, got %+v", got)
		}
	})

	t.Run("empty query returns the pool untouched", func(t *testing.T) {
		if got := FilterCandidates(pool, ""); len(got) != len(pool) {
			t.Fatalf("q=\"\": want the whole pool back, got %+v", got)
		}
	})
}

type fakeSource struct {
	list []*history.ListEntry
}

func (f fakeSource) ListEntries() []*history.ListEntry { return f.list }

// TestBuildCandidates_Universe locks the candidate universe contract: every
// recorded entry is tagged with its state at the event's time and whether it
// is a potential match for the event (shares host+path), and the view filters
// derive the matching and pending lists from it.
func TestBuildCandidates_Universe(t *testing.T) {
	cfg := mustConfig(t, `[{"match":{"host":"ads.example.com"},"ignore":true}]`)
	src := fakeSource{list: []*history.ListEntry{
		{ID: "e1", Method: "GET", URL: "https://api.example.com/a?x=1", Status: new(200)},
		{ID: "e2", Method: "GET", URL: "https://api.example.com/a?x=2", Status: new(200)},
		{ID: "e3", Method: "GET", URL: "https://api.example.com/b", Status: new(200)},
		{ID: "e4", Method: "GET", URL: "https://ads.example.com/ad.js?cb=9", Status: new(200)},
	}}
	events := []session.ReplayEvent{
		{Seq: 1, Result: "hit", EntryID: "e1", URL: "https://api.example.com/a?x=1", Method: "GET"},
	}
	ev := &session.ReplayEvent{Seq: 2, Result: "miss", URL: "https://api.example.com/a?x=2", Method: "GET"}

	universe := BuildCandidates(ev, events, cfg, src)
	if len(universe) != 4 {
		t.Fatalf("universe = %d entries, want 4: %+v", len(universe), universe)
	}

	byID := map[string]Candidate{}
	for _, c := range universe {
		byID[c.EntryID] = c
	}
	// The incoming URL shares host+path with e1 and e2 (same /a), so both are
	// potential matches; e3 (/b) and e4 (ads host) are not. e1 was consumed by
	// the seq-1 hit, e2 is pending and matches exactly (diff 0), e3 is pending
	// on another path, and e4's host is ignored by the config so it is tagged
	// ignored -- not pending.
	if c := byID["e1"]; c.Tag != "consumed" || !c.PotentialMatch {
		t.Errorf("e1 = %+v, want consumed potential match", c)
	}
	if c := byID["e2"]; c.Tag != "pending" || !c.PotentialMatch || c.DiffCount != 0 {
		t.Errorf("e2 = %+v, want pending potential match with diff 0", c)
	}
	if c := byID["e3"]; c.Tag != "pending" || c.PotentialMatch {
		t.Errorf("e3 = %+v, want pending non-match", c)
	}
	if c := byID["e4"]; c.Tag != "ignored" || c.PotentialMatch || c.DiffCount != 0 {
		t.Errorf("e4 = %+v, want ignored non-match with no diff count", c)
	}
}

// TestSelectCandidates locks the view-filter contract: the matching view
// (PotentialMatch=true) is ranked served -> consumed -> pending by diff count
// ascending, the pending view (tag=pending) keeps the universe's queue order,
// and no filter returns the whole universe.
func TestSelectCandidates(t *testing.T) {
	cfg := mustConfig(t, `[{"match":{"host":"ads.example.com"},"ignore":true}]`)
	src := fakeSource{list: []*history.ListEntry{
		{ID: "e1", Method: "GET", URL: "https://api.example.com/a?x=1", Status: new(200)},
		{ID: "e2", Method: "GET", URL: "https://api.example.com/a?x=2", Status: new(200)},
		{ID: "e3", Method: "GET", URL: "https://api.example.com/b", Status: new(200)},
		{ID: "e4", Method: "GET", URL: "https://ads.example.com/ad.js?cb=9", Status: new(200)},
	}}
	events := []session.ReplayEvent{
		{Seq: 1, Result: "hit", EntryID: "e1", URL: "https://api.example.com/a?x=1", Method: "GET"},
	}
	ev := &session.ReplayEvent{Seq: 2, Result: "miss", URL: "https://api.example.com/a?x=2", Method: "GET"}
	universe := BuildCandidates(ev, events, cfg, src)

	t.Run("matching view ranks served, consumed then pending by diff", func(t *testing.T) {
		got := SelectCandidates(universe, CandidateFilter{PotentialMatch: new(true)})
		if len(got) != 2 {
			t.Fatalf("matching = %d, want 2 (e1, e2): %+v", len(got), got)
		}
		if got[0].EntryID != "e1" || got[0].Tag != "consumed" {
			t.Errorf("matching[0] = %+v, want consumed e1 first", got[0])
		}
		if got[1].EntryID != "e2" || got[1].Tag != "pending" {
			t.Errorf("matching[1] = %+v, want pending e2", got[1])
		}
	})

	t.Run("pending view keeps queue order and excludes ignored entries", func(t *testing.T) {
		got := SelectCandidates(universe, CandidateFilter{Tag: "pending"})
		if len(got) != 2 {
			t.Fatalf("pending = %d, want 2 (e2, e3, not the ignored e4): %+v", len(got), got)
		}
		// The unconsumed entries remain in queue (ordinal) order; e1 was
		// consumed by the seq-1 hit and never reappears, and the ignored e4 is
		// not a pending entry.
		if got[0].Entry >= got[1].Entry {
			t.Errorf("pending = %+v, want queue order (Entry ascending)", got)
		}
		for _, c := range got {
			if c.EntryID == "e1" || c.EntryID == "e4" {
				t.Errorf("pending must not include consumed e1 or ignored e4, got %+v", got)
			}
		}
	})

	t.Run("no filter returns the whole universe", func(t *testing.T) {
		got := SelectCandidates(universe, CandidateFilter{})
		if len(got) != 4 {
			t.Fatalf("all = %d, want 4 entries", len(got))
		}
	})

	t.Run("counts report matching, pending and ignored sizes", func(t *testing.T) {
		got := Counts(universe)
		if got["matching"] != 2 || got["pending"] != 2 || got["ignored"] != 1 {
			t.Errorf("counts = %+v, want matching 2, pending 2, ignored 1", got)
		}
	})

	t.Run("ignored view selects exactly the ignored entries", func(t *testing.T) {
		got := SelectCandidates(universe, CandidateFilter{Tag: "ignored"})
		if len(got) != 1 || got[0].EntryID != "e4" {
			t.Fatalf("ignored = %+v, want only e4", got)
		}
	})
}

// TestSelectCandidates_IgnoredPotentialInMatching locks that an entry ignored
// by the config but sharing the event's host+path still appears in the matching
// view (PotentialMatch=true), tagged ignored so the reason it is not servable
// stays visible.
func TestSelectCandidates_IgnoredPotentialInMatching(t *testing.T) {
	universe := []Candidate{
		{EntryID: "e1", Entry: 1, URL: "https://api.example.com/a?x=1", Tag: "consumed", PotentialMatch: true},
		{EntryID: "e2", Entry: 2, URL: "https://api.example.com/a?x=2", Tag: "pending", PotentialMatch: true},
		{EntryID: "e4", Entry: 4, URL: "https://api.example.com/a?x=9", Tag: "ignored", PotentialMatch: true},
	}
	got := SelectCandidates(universe, CandidateFilter{PotentialMatch: new(true)})
	if len(got) != 3 {
		t.Fatalf("matching = %d, want the ignored potential match included: %+v", len(got), got)
	}
	found := false
	for _, c := range got {
		if c.EntryID == "e4" && c.Tag == "ignored" {
			found = true
		}
	}
	if !found {
		t.Errorf("matching view must keep the ignored potential match tagged ignored, got %+v", got)
	}
}

// TestSelectCandidate_HitServed locks that a hit always selects the served
// entry, and a miss with an exact-key consumed candidate selects that one.
func TestSelectCandidate_Picking(t *testing.T) {
	cfg := &session.MatchConfig{}
	pool := []Candidate{
		{EntryID: "e2", Entry: 2, Method: "GET", URL: "https://api.example.com/a?x=2", Tag: "consumed", ConsumedBySeq: 1},
		{EntryID: "e3", Entry: 3, Method: "GET", URL: "https://api.example.com/a?x=3", Tag: "pending", DiffCount: 1},
	}
	hit := &session.ReplayEvent{Seq: 2, Result: "hit", EntryID: "servedID", Method: "GET", URL: "https://api.example.com/a?x=2"}
	served := []Candidate{
		{EntryID: "e3", Entry: 3, Method: "GET", URL: "https://api.example.com/a?x=3", Tag: "pending", DiffCount: 1},
		{EntryID: "servedID", Entry: 4, Method: "GET", URL: "https://api.example.com/a?x=2", Tag: "served"},
	}
	if got := SelectCandidate(hit, served, cfg); got.EntryID != "servedID" {
		t.Errorf("hit: want the served entry, got %+v", got)
	}

	miss := &session.ReplayEvent{Seq: 2, Result: "miss", Method: "GET", URL: "https://api.example.com/a?x=2"}
	if got := SelectCandidate(miss, pool, cfg); got.EntryID != "e2" {
		t.Errorf("miss with exact consumed key: want the consumed e2, got %+v", got)
	}
}

// TestEventBySeq guards the event lookup helper.
func TestEventBySeq(t *testing.T) {
	events := []session.ReplayEvent{{Seq: 1}, {Seq: 3}}
	if ev := EventBySeq(events, 3); ev == nil || ev.Seq != 3 {
		t.Errorf("EventBySeq(3) = %+v, want seq 3", ev)
	}
	if ev := EventBySeq(events, 2); ev != nil {
		t.Errorf("EventBySeq(2) = %+v, want nil", ev)
	}
}

// TestBuildCandidates_ConsumedBeforeMapping checks that the consumed mapping
// only reports the earliest hit for each entry.
func TestConsumedBefore(t *testing.T) {
	events := []session.ReplayEvent{
		{Seq: 2, Result: "hit", EntryID: "e9"},
		{Seq: 4, Result: "hit", EntryID: "e9"},
		{Seq: 5, Result: "miss"},
	}
	m := ConsumedBefore(events, 4)
	ci, ok := m["e9"]
	if !ok {
		t.Fatal("e9 should be in the consumed map for thisSeq=4")
	}
	if ci.ConsumedBySeq != 2 {
		t.Errorf("e9 consumed by seq = %d, want 2 (earliest hit)", ci.ConsumedBySeq)
	}
	if _, ok := m["nope"]; ok {
		t.Error("unrelated entry should not be mapped")
	}
}
