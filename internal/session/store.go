package session

import (
	"net/url"
	"sort"
	"strings"
	"sync"

	"gospy/internal/history"
)

// ReplayStore serves recorded responses from a history-format session
// directory. It keeps per-URL groups with consumption cursors so repeated
// requests (e.g. live manifest polls) are served in recorded order.
type ReplayStore struct {
	h            *history.Store
	mu           sync.Mutex
	groups       map[string][]*history.ListEntry
	cursors      map[string]int
	groupsCfg    *MatchConfig
	groupsIdxLen int
}

func NewReplayStore(h *history.Store) *ReplayStore {
	return &ReplayStore{h: h}
}

func (r *ReplayStore) Dir() string { return r.h.Dir() }

func (r *ReplayStore) Match(method, rawURL string, cfg *MatchConfig) (*history.Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.ensureGroups(cfg)
	key := matchGroupKey(method, rawURL, cfg)
	group := r.groups[key]
	if len(group) == 0 {
		return nil, false
	}
	idx := r.cursors[key]
	if idx >= len(group) {
		return nil, true
	}
	le := group[idx]
	r.cursors[key] = idx + 1
	entry, err := r.h.Get(le.ID)
	if err != nil {
		return nil, false
	}
	return entry, false
}

func matchGroupKey(method, rawURL string, cfg *MatchConfig) string {
	return strings.ToLower(method) + "\x00" + normalizeURL(rawURL, cfg)
}

func (r *ReplayStore) ensureGroups(cfg *MatchConfig) {
	entries := r.h.ListSummary()
	if r.groups != nil && r.groupsCfg == cfg && r.groupsIdxLen == len(entries) {
		return
	}
	r.groups = make(map[string][]*history.ListEntry)
	for _, le := range entries {
		if le.Status == nil || *le.Status == 0 {
			continue
		}
		key := matchGroupKey(le.Method, le.URL, cfg)
		r.groups[key] = append(r.groups[key], le)
	}
	for key := range r.groups {
		sort.Slice(r.groups[key], func(i, j int) bool {
			return r.groups[key][i].Timestamp.Before(r.groups[key][j].Timestamp)
		})
	}
	r.cursors = make(map[string]int)
	r.groupsCfg = cfg
	r.groupsIdxLen = len(entries)
}

func normalizeURL(rawURL string, cfg *MatchConfig) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		u, err = url.Parse("http://" + rawURL)
		if err != nil {
			return rawURL
		}
	}
	if cfg != nil && len(cfg.IgnoreQueryParams) > 0 {
		q := u.Query()
		for _, key := range cfg.IgnoreQueryParams {
			q.Del(key)
		}
		u.RawQuery = q.Encode()
	}
	result := u.Scheme + "://" + strings.ToLower(u.Host) + u.Path
	if u.RawQuery != "" {
		result += "?" + u.RawQuery
	}
	return result
}
