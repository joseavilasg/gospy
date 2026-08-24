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
	// ResultIgnored: the host matches an ignore_hosts entry; no match
	// was attempted and the request was rejected with a 404.
	ResultIgnored
	// ResultRepeat: the queue had no unconsumed entry but a rule with
	// repeat_on_miss matched; the last recorded response for that rule
	// was served again.
	ResultRepeat
)

func (r MatchResult) String() string {
	switch r {
	case ResultMiss:
		return "miss"
	case ResultHit:
		return "hit"
	case ResultExhausted:
		return "exhausted"
	case ResultIgnored:
		return "ignored"
	case ResultRepeat:
		return "repeat"
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
	entry, result, _, _ := r.MatchDetailed(method, rawURL, cfg)
	return entry, result
}

// MatchDetailed is Match plus the queue context for the request: for a miss it
// returns every recorded entry still unconsumed at that moment (with the total
// count), so a failing request can be debugged against what the recording
// still had available.
func (r *ReplayStore) MatchDetailed(method, rawURL string, cfg *MatchConfig) (*history.Entry, MatchResult, []UnconsumedEntry, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	requestIgnored := IsIgnored(rawURL, cfg)

	r.ensureQueue(cfg)
	key := matchKey(method, rawURL, cfg)
	var pending []UnconsumedEntry
	pendingCount := 0
	for i, le := range r.queue {
		if r.consumed[i] {
			continue
		}
		if IsIgnored(le.URL, cfg) {
			r.consumed[i] = true
			continue
		}
		pendingCount++
		if matchKey(le.Method, le.URL, cfg) == key {
			r.consumed[i] = true
			entry, err := r.h.Get(le.ID)
			if err != nil {
				return nil, ResultMiss, pending, pendingCount
			}
			return entry, ResultHit, nil, pendingCount
		}
		pending = append(pending, UnconsumedEntry{ID: le.ID})
	}
	if pendingCount > 0 {
		if requestIgnored {
			return nil, ResultIgnored, nil, 0
		}
		return nil, ResultMiss, pending, pendingCount
	}
	if requestIgnored {
		return nil, ResultIgnored, nil, 0
	}
	return nil, ResultExhausted, nil, 0
}

// FindMatchingRuleIndex returns the index of the first rule whose match fields
// satisfy the given URL, or -1 if no rule matches.
func FindMatchingRuleIndex(rawURL string, cfg *MatchConfig) int {
	if cfg == nil {
		return -1
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return -1
	}
	host, path := strings.ToLower(u.Host), u.Path
	for i, r := range *cfg {
		if isRuleMatch(host, path, r) {
			return i
		}
	}
	return -1
}

// Progress reports how many recorded entries have been consumed, the queue
// size, and whether the queue is fully consumed (exhausted).
func (r *ReplayStore) Progress(cfg *MatchConfig) (consumed, total int, exhausted bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.ensureQueue(cfg)
	for _, c := range r.consumed {
		if c {
			consumed++
		}
	}
	total = len(r.queue)
	return consumed, total, consumed == total
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
	if cfg != nil {
		if keys := EffectiveIgnoreParams(rawURL, cfg); len(keys) > 0 {
			q := u.Query()
			for _, key := range keys {
				q.Del(key)
			}
			u.RawQuery = q.Encode()
		}
	}
	result := u.Scheme + "://" + strings.ToLower(u.Host) + u.Path
	if u.RawQuery != "" {
		result += "?" + u.RawQuery
	}
	return result
}
