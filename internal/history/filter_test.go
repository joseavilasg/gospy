package history

import (
	"testing"
)

func strPtr(s int) *int { return &s }

func testEntry() *ListEntry {
	return &ListEntry{
		ID:                  "e1",
		Method:              "GET",
		URL:                 "https://api.example.com/v1/users",
		Host:                "api.example.com",
		Status:              strPtr(200),
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
