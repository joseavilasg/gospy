package history

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Filters is the server-side filter criteria applied to the request list.
// It is the single filter implementation shared by the WebUI and any consumer
// (e.g. the agent MCP) - no client-side equivalent exists.
type Filters struct {
	Process             []string `json:"process,omitempty"`
	Referer             []string `json:"referer,omitempty"`
	Host                []string `json:"host,omitempty"`
	RequestContentType  []string `json:"requestContentType,omitempty"`
	ResponseContentType []string `json:"responseContentType,omitempty"`
	Origin              []string `json:"origin,omitempty"`
	Text                string   `json:"text,omitempty"`
	MatchMode           string   `json:"matchMode,omitempty"`

	// Body holds entry IDs produced by an on-demand deep search. It is volatile:
	// never persisted, only valid for the current process session.
	Body []string `json:"-"`
}

// HostMatcher reports whether a host matches a set of patterns (wildcard support).
type HostMatcher interface {
	Matches(host string) bool
}

// MatchOpts carries global view state the engine needs beyond the criteria.
type MatchOpts struct {
	Ignored      HostMatcher
	Focused      HostMatcher
	FocusEnabled bool
}

// Matches reports whether le is part of the filtered list, mirroring the exact
// pipeline of the previous client-side filter: ignore → focus → filter chips
// (match all/any) → free-text filter.
func (f *Filters) Matches(le *ListEntry, opts MatchOpts) bool {
	if opts.Ignored != nil && opts.Ignored.Matches(le.Host) {
		return false
	}
	if opts.FocusEnabled && (opts.Focused == nil || !opts.Focused.Matches(le.Host)) {
		return false
	}
	if !f.matchesFilters(le) {
		return false
	}
	if f.Text != "" && !matchesText(le, f.Text) {
		return false
	}
	return true
}

type typeCheck struct {
	values []string
	value  string
}

func (f *Filters) matchesFilters(le *ListEntry) bool {
	checks := make([]typeCheck, 0, 7)
	if len(f.Process) > 0 {
		checks = append(checks, typeCheck{f.Process, processName(le)})
	}
	if len(f.Referer) > 0 {
		checks = append(checks, typeCheck{f.Referer, RefererOrigin(le.Referer)})
	}
	if len(f.Host) > 0 {
		checks = append(checks, typeCheck{f.Host, le.Host})
	}
	if len(f.RequestContentType) > 0 {
		checks = append(checks, typeCheck{f.RequestContentType, le.RequestContentType})
	}
	if len(f.ResponseContentType) > 0 {
		checks = append(checks, typeCheck{f.ResponseContentType, le.ResponseContentType})
	}
	if len(f.Origin) > 0 {
		checks = append(checks, typeCheck{f.Origin, le.Origin})
	}
	if len(f.Body) > 0 {
		checks = append(checks, typeCheck{f.Body, le.ID})
	}
	if len(checks) == 0 {
		return true
	}
	if f.MatchMode == "any" {
		for _, c := range checks {
			if containsString(c.values, c.value) {
				return true
			}
		}
		return false
	}
	for _, c := range checks {
		if !containsString(c.values, c.value) {
			return false
		}
	}
	return true
}

// RefererOrigin returns the host portion of a referer URL, mirroring the
// previous client-side extractRefererOrigin.
func RefererOrigin(referer string) string {
	if referer == "" {
		return ""
	}
	u, err := url.Parse(referer)
	if err != nil {
		return ""
	}
	return u.Host
}

func processName(le *ListEntry) string {
	if le.ClientDisplayName != "" {
		return le.ClientDisplayName
	}
	return le.ClientProcess
}

func matchesText(le *ListEntry, text string) bool {
	q := strings.ToLower(text)
	url := strings.ToLower(le.URL)
	if url == "" {
		url = strings.ToLower(le.Host)
	}
	method := strings.ToLower(le.Method)
	status := ""
	if le.Status != nil {
		status = strconv.Itoa(*le.Status)
	}
	process := strings.ToLower(processName(le))
	id := strings.ToLower(le.ID)
	return strings.Contains(method, q) ||
		strings.Contains(url, q) ||
		strings.Contains(status, q) ||
		strings.Contains(process, q) ||
		strings.Contains(id, q)
}

// OptionCount is a distinct extractable value for a filter type and how many
// non-ignored entries carry it - powers the filter popover dropdown.
type OptionCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Options computes the distinct values + counts for a filter type over entries,
// mirroring the previous client-side popover options derived from the loaded list.
func Options(entries []*ListEntry, typ string, ignored HostMatcher) []OptionCount {
	counts := make(map[string]int)
	for _, le := range entries {
		if ignored != nil && ignored.Matches(le.Host) {
			continue
		}
		var val string
		switch typ {
		case "process":
			val = processName(le)
		case "referer":
			val = RefererOrigin(le.Referer)
		case "host":
			val = le.Host
		case "requestContentType":
			val = le.RequestContentType
		case "responseContentType":
			val = le.ResponseContentType
		case "origin":
			val = le.Origin
		default:
			return nil
		}
		if val == "" {
			continue
		}
		counts[val]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]OptionCount, 0, len(keys))
	for _, k := range keys {
		out = append(out, OptionCount{Value: k, Count: counts[k]})
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
