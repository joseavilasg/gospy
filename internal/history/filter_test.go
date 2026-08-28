package history

import (
	"testing"
	"time"
)

func TestPageVisibleSet(t *testing.T) {
	le := func(id, host string) *ListEntry {
		return &ListEntry{ID: id, Host: host}
	}
	entries := []*ListEntry{
		le("a", "h1"), le("b", "h2"), le("c", "h3"), le("d", "h4"), le("e", "h5"),
	}
	ignored := func(host string) bool { return host == "h2" }
	matches := func(l *ListEntry) bool { return l.Host != "h4" }

	// Page 0: skips ignored h2 and non-matching h4; total=4, visible=3.
	lastVisible := map[string]bool{}
	page, total, visible := PageVisibleSet(entries, ignored, matches, lastVisible, 0, 2)
	if total != 4 || visible != 3 {
		t.Fatalf("total=%d visible=%d, want 4/3", total, visible)
	}
	if len(page) != 2 || page[0].ID != "a" || page[1].ID != "c" {
		t.Fatalf("page 0 = %+v, want [a c]", ids(page))
	}
	for _, id := range []string{"a", "c", "e"} {
		if !lastVisible[id] {
			t.Errorf("lastVisible missing %s", id)
		}
	}
	if lastVisible["b"] || lastVisible["d"] {
		t.Error("lastVisible must exclude ignored and non-matching entries")
	}

	// Page 1: the next visible entry.
	page1, _, _ := PageVisibleSet(entries, ignored, matches, nil, 2, 2)
	if len(page1) != 1 || page1[0].ID != "e" {
		t.Fatalf("page 1 = %+v, want [e]", ids(page1))
	}

	// Offset past the end: empty page, never nil (wire serializes [] not null).
	pageEnd, total2, visible2 := PageVisibleSet(entries, ignored, matches, nil, 10, 2)
	if len(pageEnd) != 0 || pageEnd == nil {
		t.Fatalf("page past end = %#v, want non-nil empty slice", pageEnd)
	}
	if total2 != 4 || visible2 != 3 {
		t.Fatalf("total=%d visible=%d, want 4/3", total2, visible2)
	}

	// Non-positive limit: unbounded page.
	pageAll, _, _ := PageVisibleSet(entries, ignored, matches, nil, 0, 0)
	if len(pageAll) != 3 || pageAll[2].ID != "e" {
		t.Fatalf("unbounded page = %+v, want [a c e]", ids(pageAll))
	}

	// Clamped page never returns more than limit entries.
	pageClamped, _, _ := PageVisibleSet(entries, ignored, matches, nil, 0, 2)
	if len(pageClamped) != 2 {
		t.Fatalf("clamped page len = %d, want 2", len(pageClamped))
	}
}

func ids(page []*ListEntry) []string {
	out := make([]string, 0, len(page))
	for _, l := range page {
		out = append(out, l.ID)
	}
	return out
}

func testEntry() *ListEntry {
	return &ListEntry{
		ID:                  "e1",
		Method:              "GET",
		URL:                 "https://api.example.com/v1/users",
		Host:                "api.example.com",
		Status:              new(200),
		ClientDisplayName:   "chrome.exe",
		ClientProcess:       "chrome.exe",
		Referer:             "https://github.com/org/repo",
		RequestContentType:  "application/json",
		ResponseContentType: "text/html",
	}
}

type mockHostMatcher struct{ hosts map[string]bool }

func (m mockHostMatcher) Matches(host string) bool { return m.hosts[host] }

func TestFilters_Match_NoFilters(t *testing.T) {
	f := &Filters{}
	if !f.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected match with no filters")
	}
}

func TestFilters_Host(t *testing.T) {
	f := &Filters{Host: []string{"api.example.com"}}
	if !f.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected host match")
	}
	f2 := &Filters{Host: []string{"other.com"}}
	if f2.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected no match for different host")
	}
}

func TestFilters_RefererOrigin(t *testing.T) {
	f := &Filters{Referer: []string{"github.com"}}
	if !f.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected referer origin match")
	}
}

func TestFilters_Process_DisplayNameFallback(t *testing.T) {
	le := testEntry()
	le.ClientDisplayName = ""
	f := &Filters{Process: []string{"chrome.exe"}}
	if !f.Matches(le, MatchOpts{}) {
		t.Fatal("expected process match via ClientProcess fallback")
	}
}

func TestFilters_ContentTypes(t *testing.T) {
	f := &Filters{RequestContentType: []string{"application/json"}}
	if !f.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected request CT match")
	}
	f2 := &Filters{ResponseContentType: []string{"text/html"}}
	if !f2.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected response CT match")
	}
}

func TestFilters_BodyIDs(t *testing.T) {
	f := &Filters{Body: []string{"e1"}}
	if !f.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected body ID match")
	}
	f2 := &Filters{Body: []string{"other"}}
	if f2.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected no match for other body ID")
	}
}

func TestFilters_MatchModeAll(t *testing.T) {
	f := &Filters{Host: []string{"api.example.com"}, Referer: []string{"github.com"}}
	if !f.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected match for all-mode with both matching")
	}
	f2 := &Filters{Host: []string{"other.com"}, Referer: []string{"github.com"}}
	if f2.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected no match for all-mode with one non-matching")
	}
}

func TestFilters_MatchModeAny(t *testing.T) {
	f := &Filters{Host: []string{"other.com"}, Referer: []string{"github.com"}, MatchMode: "any"}
	if !f.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected match for any-mode with one matching")
	}
	f2 := &Filters{Host: []string{"other.com"}, Referer: []string{"x.com"}, MatchMode: "any"}
	if f2.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected no match for any-mode with none matching")
	}
}

func TestFilters_Text(t *testing.T) {
	f := &Filters{Text: "users"}
	if !f.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected text match in URL")
	}
	f2 := &Filters{Text: "404"}
	if f2.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected no text match")
	}
	f3 := &Filters{Text: "chrome"}
	if !f3.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected text match in process")
	}
}

func TestFilters_Text_MatchesStatus(t *testing.T) {
	f := &Filters{Text: "200"}
	if !f.Matches(testEntry(), MatchOpts{}) {
		t.Fatal("expected text match in status")
	}
	le := testEntry()
	le.Status = nil
	if f.Matches(le, MatchOpts{}) {
		t.Fatal("expected no text match for nil status")
	}
}

func TestFilters_Ignore(t *testing.T) {
	f := &Filters{}
	opts := MatchOpts{Ignored: mockHostMatcher{hosts: map[string]bool{"api.example.com": true}}}
	if f.Matches(testEntry(), opts) {
		t.Fatal("expected ignored host excluded")
	}
}

func TestFilters_Focus(t *testing.T) {
	f := &Filters{}
	opts := MatchOpts{
		FocusEnabled: true,
		Focused:      mockHostMatcher{hosts: map[string]bool{"api.example.com": true}},
	}
	if !f.Matches(testEntry(), opts) {
		t.Fatal("expected focused host match")
	}
	opts2 := MatchOpts{
		FocusEnabled: true,
		Focused:      mockHostMatcher{hosts: map[string]bool{"other.com": true}},
	}
	if f.Matches(testEntry(), opts2) {
		t.Fatal("expected non-focused host excluded")
	}
	opts3 := MatchOpts{Focused: mockHostMatcher{hosts: map[string]bool{"other.com": true}}}
	if !f.Matches(testEntry(), opts3) {
		t.Fatal("expected all hosts pass when focus disabled")
	}
}

func TestFilters_FocusIntersectsFilters(t *testing.T) {
	focused := MatchOpts{
		FocusEnabled: true,
		Focused:      mockHostMatcher{hosts: map[string]bool{"api.example.com": true}},
	}

	// Focused AND matching the active filter -> included.
	f := &Filters{Host: []string{"api.example.com"}}
	if !f.Matches(testEntry(), focused) {
		t.Fatal("expected focused host matching filter included")
	}

	// Focused but NOT matching the active filter -> excluded: filters act within the focused subset.
	f2 := &Filters{Host: []string{"other.com"}}
	if f2.Matches(testEntry(), focused) {
		t.Fatal("expected focused host failing filter excluded")
	}

	// Matching the filter but NOT focused -> excluded: focus gates first.
	le := testEntry()
	le.Host = "other.com"
	if f.Matches(le, focused) {
		t.Fatal("expected non-focused host excluded even when it matches the filter")
	}

	// Match mode 'any' still cannot escape the focus gate.
	f3 := &Filters{Host: []string{"other.com"}, MatchMode: "any"}
	if f3.Matches(le, focused) {
		t.Fatal("expected focus gate to hold under matchMode any")
	}
}

func TestRefererOrigin(t *testing.T) {
	cases := map[string]string{
		"":                            "",
		"https://github.com/org/repo": "github.com",
		"http://localhost:8080/x":     "localhost:8080",
		":::not-a-url:::":             "",
	}
	for in, want := range cases {
		if got := RefererOrigin(in); got != want {
			t.Errorf("RefererOrigin(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOptions(t *testing.T) {
	entries := []*ListEntry{
		testEntry(),
		func() *ListEntry { le := testEntry(); le.ID = "e2"; le.Host = "other.com"; return le }(),
		func() *ListEntry {
			le := testEntry()
			le.ID = "e3"
			le.Host = "api.example.com"
			le.Referer = ""
			return le
		}(),
	}
	opts := Options(entries, "host", nil)
	if len(opts) != 2 {
		t.Fatalf("expected 2 distinct hosts, got %d", len(opts))
	}
	if opts[0].Value != "api.example.com" || opts[0].Count != 2 {
		t.Errorf("unexpected first option: %+v", opts[0])
	}
	if opts[1].Value != "other.com" || opts[1].Count != 1 {
		t.Errorf("unexpected second option: %+v", opts[1])
	}

	refOpts := Options(entries, "referer", nil)
	if len(refOpts) != 1 || refOpts[0].Count != 2 {
		t.Errorf("unexpected referer options: %+v", refOpts)
	}
}

func TestOptions_IgnoresIgnoredHosts(t *testing.T) {
	entries := []*ListEntry{testEntry(), func() *ListEntry { le := testEntry(); le.ID = "e2"; le.Host = "other.com"; return le }()}
	opts := Options(entries, "host", mockHostMatcher{hosts: map[string]bool{"other.com": true}})
	if len(opts) != 1 || opts[0].Value != "api.example.com" {
		t.Fatalf("expected ignored host excluded from options: %+v", opts)
	}
}

func TestFilters_Origin(t *testing.T) {
	agent := testEntry()
	agent.Origin = "agent"
	browser := testEntry()
	browser.ID = "e2"
	browser.Origin = ""

	f := &Filters{Origin: []string{"agent"}}
	if !f.Matches(agent, MatchOpts{}) {
		t.Fatal("expected origin agent match")
	}
	if f.Matches(browser, MatchOpts{}) {
		t.Fatal("expected no match for browser entry with origin=agent filter")
	}

	// Origin is just another filter type: participates in all/any mode.
	f2 := &Filters{Origin: []string{"agent"}, Host: []string{"other.com"}, MatchMode: "any"}
	if !f2.Matches(agent, MatchOpts{}) {
		t.Fatal("expected any-mode match via origin")
	}
	f3 := &Filters{Origin: []string{"agent"}, Host: []string{"other.com"}}
	if f3.Matches(agent, MatchOpts{}) {
		t.Fatal("expected no all-mode match when host doesn't match")
	}
}

func TestFilters_AgentOriginIsFiltered(t *testing.T) {
	agent := testEntry()
	agent.Origin = "agent"

	// An agent-origin entry violating the current filter criteria is NOT
	// visible: the same filter scope that bounds browser traffic bounds the
	// agent's own traffic (github/sonarqube scenario).
	f := &Filters{Host: []string{"sonarqube.com"}}
	if f.Matches(agent, MatchOpts{}) {
		t.Fatal("expected agent-origin entry filtered out by host criteria")
	}

	// An agent-origin entry matching the filter IS visible.
	fMatching := &Filters{Host: []string{"api.example.com"}}
	if !fMatching.Matches(agent, MatchOpts{}) {
		t.Fatal("expected agent-origin entry matching the filter to be visible")
	}

	// The focus gate applies to agent entries exactly like any other.
	optsFocus := MatchOpts{
		FocusEnabled: true,
		Focused:      mockHostMatcher{hosts: map[string]bool{"sonarqube.com": true}},
	}
	if fMatching.Matches(agent, optsFocus) {
		t.Fatal("expected agent-origin entry not bypassing focus")
	}

	// Ignore stays a hard pre-filter.
	optsIgnored := MatchOpts{Ignored: mockHostMatcher{hosts: map[string]bool{"api.example.com": true}}}
	if fMatching.Matches(agent, optsIgnored) {
		t.Fatal("expected ignored host excluded even for an agent-origin entry")
	}
}

func TestOptions_Origin(t *testing.T) {
	entries := []*ListEntry{
		func() *ListEntry { le := testEntry(); le.Origin = "agent"; return le }(),
		func() *ListEntry { le := testEntry(); le.ID = "e2"; le.Origin = "agent"; return le }(),
		func() *ListEntry { le := testEntry(); le.ID = "e3"; le.Origin = ""; return le }(),
	}
	opts := Options(entries, "origin", nil)
	if len(opts) != 1 || opts[0].Value != "agent" || opts[0].Count != 2 {
		t.Fatalf("expected only agent origin with count 2, got %+v", opts)
	}
}

func TestFilters_MethodPathStatus(t *testing.T) {
	entries := []*ListEntry{
		testEntry(), // GET https://api.example.com/v1/users, 200
		func() *ListEntry {
			le := testEntry()
			le.ID = "e2"
			le.Method = "POST"
			le.URL = "https://api.example.com/v1/users?action=create"
			return le
		}(),
		func() *ListEntry {
			le := testEntry()
			le.ID = "e3"
			le.Status = nil
			le.URL = "https://other.example.com/v2/files"
			return le
		}(),
	}
	opts := MatchOpts{}

	// Method: case-insensitive exact, never substring.
	f := Filters{Method: []string{"get"}, MatchMode: "all"}
	if !f.Matches(entries[0], opts) || f.Matches(entries[1], opts) {
		t.Error("method filter must be case-insensitive and exact")
	}
	// Method multi-value ORs within the field.
	f = Filters{Method: []string{"POST", "PATCH"}, MatchMode: "all"}
	if !f.Matches(entries[1], opts) || f.Matches(entries[0], opts) {
		t.Error("method multi-value must OR within the field")
	}

	// Path: case-insensitive substring over path+query.
	f = Filters{Path: []string{"/v1/USERS"}, MatchMode: "all"}
	if !f.Matches(entries[0], opts) || !f.Matches(entries[1], opts) {
		t.Error("path filter must substring-match path+query, case-insensitive")
	}
	if f.Matches(entries[2], opts) {
		t.Error("path filter must not match an unrelated URL")
	}
	// Path falls back to the raw URL when it does not parse.
	weird := &ListEntry{Method: "GET", URL: "not a url: /v1/users", Host: "x"}
	pathWeird := Filters{Path: []string{"/v1/users"}, MatchMode: "all"}
	if !pathWeird.Matches(weird, opts) {
		t.Error("path filter must fall back to the raw URL when unparseable")
	}

	// Status: exact; nil status never matches.
	f = Filters{Status: []string{"200"}, MatchMode: "all"}
	if !f.Matches(entries[0], opts) || f.Matches(entries[2], opts) {
		t.Error("status filter must match exactly and never match nil status")
	}
	// Status multi-value ORs within the field.
	f = Filters{Status: []string{"200", "404"}, MatchMode: "all"}
	if !f.Matches(entries[0], opts) || f.Matches(entries[2], opts) {
		t.Error("status multi-value must OR within the field")
	}

	// Empty must include the new fields.
	for name, f := range map[string]Filters{
		"method": {Method: []string{"GET"}},
		"path":   {Path: []string{"/api/"}},
		"status": {Status: []string{"200"}},
	} {
		if f.Empty() {
			t.Errorf("Empty must report active %s", name)
		}
	}
	emptyAll := Filters{}
	if !emptyAll.Empty() {
		t.Error("fully empty Filters must be Empty")
	}
}

func TestOptions_Method(t *testing.T) {
	entries := []*ListEntry{
		testEntry(),
		func() *ListEntry { le := testEntry(); le.ID = "e2"; le.Method = "POST"; return le }(),
		func() *ListEntry { le := testEntry(); le.ID = "e3"; le.Method = "GET"; return le }(),
	}
	opts := Options(entries, "method", nil)
	if len(opts) != 2 || opts[0].Value != "GET" || opts[0].Count != 2 || opts[1].Value != "POST" || opts[1].Count != 1 {
		t.Fatalf("Options(method) = %+v, want GET x2, POST x1", opts)
	}
	if Options(entries, "bogus", nil) != nil {
		t.Error("unknown type must return nil")
	}
}

func TestParseFilterTime(t *testing.T) {
	if _, err := ParseFilterTime("2026-08-02T14:30"); err != nil {
		t.Errorf("local wall-clock format: %v", err)
	}
	if _, err := ParseFilterTime("2026-08-02T14:30:00Z"); err != nil {
		t.Errorf("RFC3339 format: %v", err)
	}
	if _, err := ParseFilterTime("2026-08-02T14:30:00-03:00"); err != nil {
		t.Errorf("RFC3339 with offset: %v", err)
	}
	if _, err := ParseFilterTime("bogus"); err == nil {
		t.Error("malformed input must error")
	}
}

func TestFilters_TimeRange(t *testing.T) {
	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.Local)
	entries := []*ListEntry{
		{ID: "before", Timestamp: base.Add(-2 * time.Hour)},
		{ID: "at-from", Timestamp: base},
		{ID: "inside", Timestamp: base.Add(2 * time.Hour)},
		{ID: "at-to", Timestamp: base.Add(4 * time.Hour)},
		{ID: "after", Timestamp: base.Add(6 * time.Hour)},
	}
	all := []string{"before", "at-from", "inside", "at-to", "after"}

	cases := []struct {
		name string
		from string
		to   string
		want []string
	}{
		{"no-range", "", "", all},
		{"rfc3339-inclusive", base.Format(time.RFC3339), base.Add(4 * time.Hour).Format(time.RFC3339), []string{"at-from", "inside", "at-to"}},
		{"rfc3339-from-only", base.Format(time.RFC3339), "", []string{"at-from", "inside", "at-to", "after"}},
		{"rfc3339-to-only", "", base.Add(4 * time.Hour).Format(time.RFC3339), []string{"before", "at-from", "inside", "at-to"}},
		{"utc-instant", base.Add(-2 * time.Hour).UTC().Format(time.RFC3339), "", all},
		{"local-wallclock", base.Format("2006-01-02T15:04"), base.Add(4 * time.Hour).Format("2006-01-02T15:04"), []string{"at-from", "inside", "at-to"}},
		{"unparseable-ignored", "garbage", "", all},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := Filters{From: c.from, To: c.to}
			var got []string
			for _, le := range entries {
				if f.Matches(le, MatchOpts{}) {
					got = append(got, le.ID)
				}
			}
			if !slicesEqual(got, c.want) {
				t.Fatalf("From=%q To=%q matched %v, want %v", c.from, c.to, got, c.want)
			}
		})
	}
}

func TestFilters_EmptyWithTimeRange(t *testing.T) {
	f := Filters{From: "2026-08-02T10:00"}
	if f.Empty() {
		t.Error("a filter with only From must not be Empty")
	}
	var empty Filters
	if !empty.Empty() {
		t.Error("a fully empty filter must be Empty")
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
