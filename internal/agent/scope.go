package agent

import (
	"sort"

	"gospy/internal/history"
)

// HistoryStore is the minimal history access the agent scope needs.
type HistoryStore interface {
	ListSummary() []*history.ListEntry
	Get(id string) (*history.Entry, error)
	GetByAgentCallID(callID string) (*history.ListEntry, error)
	Dir() string
}

// FilterStore is the minimal filter-store access the agent scope needs.
type FilterStore interface {
	SnapshotAgent() (history.Filters, bool, int)
	AgentGate() bool
}

// Scope resolves the agent-visible set: the gate must be on and the agent filter
// profile applies. Agent-origin entries are filtered exactly like any other
// entry - there is no origin bypass.
type Scope struct {
	hist    HistoryStore
	filter  FilterStore
	ignored history.HostMatcher
	focused history.HostMatcher
}

func NewScope(hist HistoryStore, filter FilterStore, ignored, focused history.HostMatcher) *Scope {
	return &Scope{hist: hist, filter: filter, ignored: ignored, focused: focused}
}

// MCP pagination bounds: default page size 50, hard cap 200 (non-bypassable).
const (
	defaultPageSize = 50
	maxPageSize     = 200
)

func pageLimits(offset, limit int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	return offset, limit
}

// GateEnabled reports whether the agent MCP gate is on.
func (sc *Scope) GateEnabled() bool {
	return sc.filter.AgentGate()
}

func (sc *Scope) base() (all []*history.ListEntry, filters history.Filters, opts history.MatchOpts) {
	all = sc.hist.ListSummary()
	filters, focusEnabled, _ := sc.filter.SnapshotAgent()
	opts = history.MatchOpts{
		Ignored:      sc.ignored,
		Focused:      sc.focused,
		FocusEnabled: focusEnabled,
	}
	return all, filters, opts
}

// ListEntries returns page [offset, offset+limit) of the agent-visible set.
// The gate is a hard stop: when off, nothing is exposed.
func (sc *Scope) ListEntries(offset, limit int) ListPage {
	if !sc.filter.AgentGate() {
		return ListPage{Entries: make([]*AgentEntry, 0)}
	}
	all, filters, opts := sc.base()
	offset, limit = pageLimits(offset, limit)
	page, total, visibleCount := history.PageVisibleSet(all,
		func(host string) bool { return sc.ignored != nil && sc.ignored.Matches(host) },
		func(le *history.ListEntry) bool { return filters.Matches(le, opts) },
		nil, offset, limit)
	out := make([]*AgentEntry, 0, len(page))
	for _, le := range page {
		out = append(out, toAgentEntry(le))
	}
	return ListPage{
		Entries:      out,
		Total:        total,
		VisibleCount: visibleCount,
		Offset:       offset,
		HasMore:      offset+len(page) < visibleCount,
	}
}

// IsVisible gates get_entry: only entries in the agent-visible set can be read.
func (sc *Scope) IsVisible(id string) bool {
	if !sc.filter.AgentGate() {
		return false
	}
	all, filters, opts := sc.base()
	visible := make(map[string]bool, len(all))
	history.PageVisibleSet(all,
		func(host string) bool { return sc.ignored != nil && sc.ignored.Matches(host) },
		func(le *history.ListEntry) bool { return filters.Matches(le, opts) },
		visible, 0, 1)
	return visible[id]
}

// VisibleHosts returns the distinct hosts of the visible set, sorted.
func (sc *Scope) VisibleHosts() []string {
	if !sc.filter.AgentGate() {
		return []string{}
	}
	all, filters, opts := sc.base()
	set := make(map[string]bool)
	for _, le := range all {
		if sc.ignored != nil && sc.ignored.Matches(le.Host) {
			continue
		}
		if filters.Matches(le, opts) {
			set[le.Host] = true
		}
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

func toAgentEntry(le *history.ListEntry) *AgentEntry {
	return &AgentEntry{
		ID:                  le.ID,
		Timestamp:           le.Timestamp,
		UpdatedAt:           le.UpdatedAt,
		Method:              le.Method,
		URL:                 le.URL,
		Host:                le.Host,
		Status:              le.Status,
		Origin:              le.Origin,
		AppliedAction:       le.AppliedAction,
		RuleName:            le.RuleName,
		RequestContentType:  le.RequestContentType,
		ResponseContentType: le.ResponseContentType,
	}
}
