package session

import (
	"net/url"
	"sort"
	"strings"
	"sync"

	"gospy/internal/history"
)

// MatchResult describes the outcome of a replay Match.
type MatchResult int

const (
	// ResultMiss: no recorded entry matches this request but the session still
	// has unconsumed entries.
	ResultMiss MatchResult = iota
	// ResultHit: a recorded entry matched and was consumed.
	ResultHit
	// ResultExhausted: every recorded entry has already been consumed.
	ResultExhausted
)

func (r MatchResult) String() string {
	switch r {
	case ResultMiss:
		return "miss"
	case ResultHit:
		return "hit"
	case ResultExhausted:
		return "exhausted"
	default:
		return "unknown"
	}
}

// ReplayStore serves recorded responses from a history-format session
// directory. It keeps a single global queue of recorded entries in recorded
// order; each Match scans the queue and consumes the first unconsumed entry
// that matches, so repeated requests (e.g. live manifest polls) are served in
// recorded order and the session is exhausted only when the whole queue is
// consumed.
type ReplayStore struct {
	h          *history.Store
	mu         sync.Mutex
	queue      []*history.ListEntry
	consumed   []bool
	queueCfg   *MatchConfig
	queueIdxLn int
}

func NewReplayStore(h *history.Store) *ReplayStore {
	return &ReplayStore{h: h}
}

func (r *ReplayStore) Dir() string { return r.h.Dir() }

func (r *ReplayStore) Match(method, rawURL string, cfg *MatchConfig) (*history.Entry, MatchResult) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.ensureQueue(cfg)
	key := matchKey(method, rawURL, cfg)
	unconsumed := false
	for i, le := range r.queue {
		if r.consumed[i] {
			continue
		}
		unconsumed = true
		if matchKey(le.Method, le.URL, cfg) == key {
			r.consumed[i] = true
			entry, err := r.h.Get(le.ID)
			if err != nil {
				return nil, ResultMiss
			}
			return entry, ResultHit
		}
	}
	if unconsumed {
		return nil, ResultMiss
	}
	return nil, ResultExhausted
}

func matchKey(method, rawURL string, cfg *MatchConfig) string {
	return strings.ToLower(method) + "\x00" + normalizeURL(rawURL, cfg)
}

func (r *ReplayStore) ensureQueue(cfg *MatchConfig) {
	entries := r.h.ListSummary()
	if r.queue != nil && r.queueCfg == cfg && r.queueIdxLn == len(entries) {
		return
	}
	queue := make([]*history.ListEntry, 0, len(entries))
	for _, le := range entries {
		if le.Status == nil || *le.Status == 0 {
			continue
		}
		queue = append(queue, le)
	}
	sort.SliceStable(queue, func(i, j int) bool {
		return queue[i].Timestamp.Before(queue[j].Timestamp)
	})
	r.queue = queue
	r.consumed = make([]bool, len(queue))
	r.queueCfg = cfg
	r.queueIdxLn = len(entries)
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
