package session

import (
	"encoding/json"
	"net/url"
	"os"
	"strings"
)

// matchKind discriminates the kind of pattern a Match carries.
type matchKind int

const (
	matchNone matchKind = iota
	matchExact
	matchPrefix
	matchSuffix
)

// Match is a discriminated pattern for matching a single field (host or path).
// In JSON it accepts either a plain string (shorthand for exact) or an object
// with exactly one key: {"exact": "..."}, {"prefix": "..."}, or {"suffix": "..."}.
// An empty object {} is a wildcard that matches everything.
type Match struct {
	kind matchKind
	val  string
}

// IsZero reports whether the match is unset (wildcard).
func (m Match) IsZero() bool { return m.kind == matchNone }

// Type returns "exact", "prefix", "suffix", or "" for wildcard.
func (m Match) Type() string {
	switch m.kind {
	case matchExact:
		return "exact"
	case matchPrefix:
		return "prefix"
	case matchSuffix:
		return "suffix"
	default:
		return ""
	}
}

// Value returns the pattern string.
func (m Match) Value() string { return m.val }

// Matches reports whether s satisfies this pattern.
func (m Match) Matches(s string) bool {
	switch m.kind {
	case matchExact:
		return s == m.val
	case matchPrefix:
		return strings.HasPrefix(s, m.val)
	case matchSuffix:
		return strings.HasSuffix(s, m.val)
	default:
		return true
	}
}

// UnmarshalJSON implements json.Unmarshaler.
func (m *Match) UnmarshalJSON(data []byte) error {
	// Form 1: plain string → shorthand for exact.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		m.kind = matchExact
		m.val = s
		return nil
	}

	// Form 2: object with one key.
	var obj struct {
		Exact  string `json:"exact"`
		Prefix string `json:"prefix"`
		Suffix string `json:"suffix"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	switch {
	case obj.Exact != "":
		m.kind, m.val = matchExact, obj.Exact
	case obj.Prefix != "":
		m.kind, m.val = matchPrefix, obj.Prefix
	case obj.Suffix != "":
		m.kind, m.val = matchSuffix, obj.Suffix
	default:
		m.kind = matchNone
	}
	return nil
}

// MarshalJSON implements json.Marshaler.
func (m Match) MarshalJSON() ([]byte, error) {
	switch m.kind {
	case matchExact:
		return json.Marshal(m.val)
	case matchPrefix:
		return json.Marshal(struct {
			Prefix string `json:"prefix"`
		}{m.val})
	case matchSuffix:
		return json.Marshal(struct {
			Suffix string `json:"suffix"`
		}{m.val})
	default:
		return json.Marshal(struct{}{})
	}
}

// MatchFields holds optional host and path constraints for a MatchRule.
type MatchFields struct {
	Host *Match `json:"host,omitempty"`
	Path *Match `json:"path,omitempty"`
}

// MatchRule is one row in a MatchConfig array.
type MatchRule struct {
	Match             MatchFields `json:"match"`
	Ignore            bool        `json:"ignore,omitempty"`
	IgnoreQueryParams []string    `json:"ignore_query_params,omitempty"`
	RepeatOnMiss      bool        `json:"repeat_on_miss,omitempty"`
}

// MatchConfig is the ordered list of match rules applied during replay.
type MatchConfig []MatchRule

// matchField reports whether value satisfies the field constraint.
// A nil or zero-value Match is a wildcard (matches everything).
func matchField(value string, m *Match) bool {
	if m == nil || m.IsZero() {
		return true
	}
	return m.Matches(value)
}

// isRuleMatch reports whether the rule's match fields are satisfied by the
// given host and path. Host comparison is always case-insensitive.
func isRuleMatch(host, path string, rule MatchRule) bool {
	host = strings.ToLower(host)
	var hostMatch *Match
	if rule.Match.Host != nil && !rule.Match.Host.IsZero() {
		hostMatch = &Match{kind: rule.Match.Host.kind, val: strings.ToLower(rule.Match.Host.val)}
	}
	return matchField(host, hostMatch) && matchField(path, rule.Match.Path)
}

// IsIgnored reports whether any rule with Ignore=true matches the given URL.
func IsIgnored(rawURL string, cfg *MatchConfig) bool {
	if cfg == nil || len(*cfg) == 0 {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host, path := strings.ToLower(u.Host), u.Path
	for _, r := range *cfg {
		if r.Ignore && isRuleMatch(host, path, r) {
			return true
		}
	}
	return false
}

// EffectiveIgnoreParams returns the deduplicated set of query-param keys that
// should be stripped before computing a match key for the given URL. It is the
// union of ignore_query_params across all rules whose match fields are
// satisfied by the URL's host and path.
func EffectiveIgnoreParams(rawURL string, cfg *MatchConfig) []string {
	if cfg == nil || len(*cfg) == 0 {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	host, path := strings.ToLower(u.Host), u.Path
	seen := make(map[string]bool)
	var out []string
	for _, r := range *cfg {
		if len(r.IgnoreQueryParams) == 0 {
			continue
		}
		if !isRuleMatch(host, path, r) {
			continue
		}
		for _, k := range r.IgnoreQueryParams {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}

func LoadMatchConfig(path string) (*MatchConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg MatchConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
