package replay

import (
	"testing"

	"gospy/internal/history"
	"gospy/internal/session"
)

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
	src := fakeSource{list: []*history.ListEntry{
		{ID: "e1", Method: "GET", URL: "https://api.example.com/a?x=1", Status: new(200)},
		{ID: "e2", Method: "GET", URL: "https://api.example.com/a?x=2", Status: new(200)},
		{ID: "e3", Method: "GET", URL: "https://api.example.com/b", Status: new(200)},
	}}
	events := []session.ReplayEvent{
		{Seq: 1, Result: "hit", EntryID: "e1", URL: "https://api.example.com/a?x=1", Method: "GET"},
	}
	ev := &session.ReplayEvent{Seq: 2, Result: "miss", URL: "https://api.example.com/a?x=2", Method: "GET"}
	cfg := &session.MatchConfig{}

	universe := BuildCandidates(ev, events, cfg, src)
	if len(universe) != 3 {
		t.Fatalf("universe = %d entries, want 3: %+v", len(universe), universe)
	}

	byID := map[string]Candidate{}
	for _, c := range universe {
		byID[c.EntryID] = c
	}
	// The incoming URL shares host+path with e1 and e2 (same /a), so both are
	// potential matches; e3 (/b) is not. e1 was consumed by the seq-1 hit, e2
	// is pending and matches exactly (diff 0), e3 is pending on another path.
	if c := byID["e1"]; c.Tag != "consumed" || !c.PotentialMatch {
		t.Errorf("e1 = %+v, want consumed potential match", c)
	}
	if c := byID["e2"]; c.Tag != "pending" || !c.PotentialMatch || c.DiffCount != 0 {
		t.Errorf("e2 = %+v, want pending potential match with diff 0", c)
	}
	if c := byID["e3"]; c.Tag != "pending" || c.PotentialMatch {
		t.Errorf("e3 = %+v, want pending non-match", c)
	}
}

// TestSelectCandidates locks the view-filter contract: the matching view
// (PotentialMatch=true) is ranked served -> consumed -> pending by diff count
// ascending, the pending view (tag=pending) keeps the universe's queue order,
// and no filter returns the whole universe.
func TestSelectCandidates(t *testing.T) {
	src := fakeSource{list: []*history.ListEntry{
		{ID: "e1", Method: "GET", URL: "https://api.example.com/a?x=1", Status: new(200)},
		{ID: "e2", Method: "GET", URL: "https://api.example.com/a?x=2", Status: new(200)},
		{ID: "e3", Method: "GET", URL: "https://api.example.com/b", Status: new(200)},
	}}
	events := []session.ReplayEvent{
		{Seq: 1, Result: "hit", EntryID: "e1", URL: "https://api.example.com/a?x=1", Method: "GET"},
	}
	ev := &session.ReplayEvent{Seq: 2, Result: "miss", URL: "https://api.example.com/a?x=2", Method: "GET"}
	cfg := &session.MatchConfig{}
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

	t.Run("pending view keeps queue order over all pending entries", func(t *testing.T) {
		got := SelectCandidates(universe, CandidateFilter{Tag: "pending"})
		if len(got) != 2 {
			t.Fatalf("pending = %d, want 2: %+v", len(got), got)
		}
		// The unconsumed entries remain in queue (ordinal) order; e1 was
		// consumed by the seq-1 hit and never reappears.
		if got[0].Entry >= got[1].Entry {
			t.Errorf("pending = %+v, want queue order (Entry ascending)", got)
		}
		for _, c := range got {
			if c.EntryID == "e1" {
				t.Error("pending must not include the consumed e1")
			}
		}
	})

	t.Run("no filter returns the whole universe", func(t *testing.T) {
		got := SelectCandidates(universe, CandidateFilter{})
		if len(got) != 3 {
			t.Fatalf("all = %d, want 3 entries", len(got))
		}
	})

	t.Run("counts report matching and pending sizes", func(t *testing.T) {
		got := Counts(universe)
		if got["matching"] != 2 || got["pending"] != 2 {
			t.Errorf("counts = %+v, want matching 2, pending 2", got)
		}
	})
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
