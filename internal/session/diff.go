package session

import (
	"net/url"
	"sort"
	"strings"
)

// ClientOrigin describes the client process that reached the replay server,
// resolved from the incoming connection. It mirrors the process fields the
// interceptor captures for normal entries so the Origin tab can render a
// replay event identically.
type ClientOrigin struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	PID         uint32 `json:"pid"`
	DisplayName string `json:"displayName,omitempty"`
}

// OriginResolver resolves the client process of an incoming replay request by
// its remote TCP address. main wires it to a client resolver configured with
// the replay listen address.
type OriginResolver func(remoteAddr string) *ClientOrigin

// DiffStatus is the per-parameter outcome of a URL diff.
type DiffStatus string

const (
	DiffMatch           DiffStatus = "match"
	DiffMismatch        DiffStatus = "mismatch"
	DiffMissingIncoming DiffStatus = "missing_in_incoming"
	DiffMissingRecorded DiffStatus = "missing_in_recorded"
	DiffIgnored         DiffStatus = "ignored"
)

// DiffParam is one query parameter of the diff. Values are decoded for
// display; the status compares the canonical encoded values so it always
// agrees with the match decision of matchKey.
type DiffParam struct {
	Key      string     `json:"key"`
	Incoming *string    `json:"incoming,omitempty"`
	Recorded *string    `json:"recorded,omitempty"`
	Status   DiffStatus `json:"status"`
}

// DiffHostPath is the normalized scheme://host/path of both sides of the diff.
type DiffHostPath struct {
	Incoming string `json:"incoming"`
	Recorded string `json:"recorded"`
	Match    bool   `json:"match"`
}

// DiffResult describes how an incoming URL compares against a recorded one.
// DiffCount counts the params that would break the match: mismatches and
// missing params, never matched or ignored ones.
type DiffResult struct {
	HostPath  DiffHostPath `json:"hostPath"`
	Params    []DiffParam  `json:"params"`
	DiffCount int          `json:"diffCount"`
}

type pair struct{ key, val string }

// decompose canonicalizes a URL into its normalized host+path and its query
// pairs. The canonicalization mirrors matchKey exactly (scheme fallback, host
// lowercase, ignore_query_params stripped, query re-encoded when ignores are
// configured) so the diff statuses always agree with the match decision.
// Ignored pairs are captured separately, in decoded form, for display.
func decompose(rawURL string, cfg *MatchConfig) (hostPath string, pairs, ignored []pair) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		if u2, err2 := url.Parse("http://" + rawURL); err2 == nil {
			u = u2
		} else {
			return rawURL, nil, nil
		}
	}

	ignore := make(map[string]bool)
	if cfg != nil {
		for _, k := range cfg.IgnoreQueryParams {
			ignore[k] = true
		}
	}

	if len(ignore) > 0 {
		q := u.Query()
		for k, vals := range q {
			if ignore[k] {
				for _, v := range vals {
					ignored = append(ignored, pair{key: k, val: v})
				}
			}
		}
		kept := url.Values{}
		for k, vals := range q {
			if !ignore[k] {
				for _, v := range vals {
					kept.Add(k, v)
				}
			}
		}
		u.RawQuery = kept.Encode()
	}

	for _, seg := range strings.Split(u.RawQuery, "&") {
		if seg == "" {
			continue
		}
		k, v, _ := strings.Cut(seg, "=")
		if !ignore[k] {
			pairs = append(pairs, pair{key: k, val: v})
		}
	}

	hostPath = u.Scheme + "://" + strings.ToLower(u.Host) + u.Path
	return hostPath, pairs, ignored
}

// MatchKey returns the canonical matching key of a request - the key the
// replay queue matches on (lowercase method + normalized URL).
func MatchKey(method, rawURL string, cfg *MatchConfig) string {
	return matchKey(method, rawURL, cfg)
}

// HostPathKey returns the normalized scheme://host/path of a URL, the basis of
// the "Matching" candidate scope.
func HostPathKey(rawURL string, cfg *MatchConfig) string {
	hp, _, _ := decompose(rawURL, cfg)
	return hp
}

// DiffURL compares an incoming URL against a recorded one under a match
// config, returning a per-parameter diff and a count of the params that would
// break the match. Two URLs with DiffCount == 0 and a matching host+path have
// equal canonical match keys (excluding the method), so the diff agrees with
// the queue's match decision.
func DiffURL(incomingURL, recordedURL string, cfg *MatchConfig) *DiffResult {
	incHost, incPairs, incIgnored := decompose(incomingURL, cfg)
	recHost, recPairs, recIgnored := decompose(recordedURL, cfg)

	res := &DiffResult{
		HostPath: DiffHostPath{Incoming: incHost, Recorded: recHost, Match: incHost == recHost},
	}

	// Phase A consumes the pairs both sides share (same key + canonical value)
	// as matches, leaving each side's unmatched values to pair up as diffs.
	recLeft := make([]pair, len(recPairs))
	copy(recLeft, recPairs)
	incMatched := make([]bool, len(incPairs))
	for i, p := range incPairs {
		idx := -1
		for j, rp := range recLeft {
			if rp == p {
				idx = j
				break
			}
		}
		if idx >= 0 {
			recLeft = append(recLeft[:idx], recLeft[idx+1:]...)
			incMatched[i] = true
			v := decodePair(p.val)
			res.Params = append(res.Params, DiffParam{Key: p.key, Incoming: &v, Recorded: &v, Status: DiffMatch})
		}
	}

	// Phase B pairs the leftover values by key: a leftover pairs with the
	// other side's leftover value of the same key as a mismatch, or stands
	// alone as missing on that side.
	recLeftByKey := make(map[string][]string)
	for _, p := range recLeft {
		recLeftByKey[p.key] = append(recLeftByKey[p.key], p.val)
	}
	incLeftByKey := make(map[string][]string)
	var incLeft []pair
	for i, p := range incPairs {
		if !incMatched[i] {
			incLeft = append(incLeft, p)
			incLeftByKey[p.key] = append(incLeftByKey[p.key], p.val)
		}
	}
	consumedRec := make(map[pair]bool)

	for _, p := range incLeft {
		rv := recLeftByKey[p.key]
		if len(rv) > 0 {
			inc := decodePair(p.val)
			rec := decodePair(rv[0])
			consumedRec[pair{key: p.key, val: rv[0]}] = true
			recLeftByKey[p.key] = rv[1:]
			res.Params = append(res.Params, DiffParam{Key: p.key, Incoming: &inc, Recorded: &rec, Status: DiffMismatch})
		} else {
			inc := decodePair(p.val)
			res.Params = append(res.Params, DiffParam{Key: p.key, Incoming: &inc, Status: DiffMissingRecorded})
		}
		res.DiffCount++
	}
	for _, p := range recLeft {
		if consumedRec[p] {
			continue
		}
		iv := incLeftByKey[p.key]
		if len(iv) > 0 {
			inc := decodePair(iv[0])
			rec := decodePair(p.val)
			incLeftByKey[p.key] = iv[1:]
			res.Params = append(res.Params, DiffParam{Key: p.key, Incoming: &inc, Recorded: &rec, Status: DiffMismatch})
		} else {
			rec := decodePair(p.val)
			res.Params = append(res.Params, DiffParam{Key: p.key, Recorded: &rec, Status: DiffMissingIncoming})
		}
		res.DiffCount++
	}

	// Ignored params are informational: stripped from the match, shown struck
	// through with both sides when present.
	ignoredKeys := make(map[string]bool)
	for _, p := range incIgnored {
		ignoredKeys[p.key] = true
	}
	for _, p := range recIgnored {
		ignoredKeys[p.key] = true
	}
	keys := make([]string, 0, len(ignoredKeys))
	for k := range ignoredKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var incVals, recVals []string
		for _, p := range incIgnored {
			if p.key == k {
				incVals = append(incVals, p.val)
			}
		}
		for _, p := range recIgnored {
			if p.key == k {
				recVals = append(recVals, p.val)
			}
		}
		row := DiffParam{Key: k, Status: DiffIgnored}
		if len(incVals) > 0 {
			v := strings.Join(incVals, ", ")
			row.Incoming = &v
		}
		if len(recVals) > 0 {
			v := strings.Join(recVals, ", ")
			row.Recorded = &v
		}
		res.Params = append(res.Params, row)
	}

	return res
}

func decodePair(v string) string {
	if d, err := url.QueryUnescape(v); err == nil {
		return d
	}
	return v
}
