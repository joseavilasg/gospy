package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMatchUnmarshalString(t *testing.T) {
	var m Match
	if err := json.Unmarshal([]byte(`"exact"`), &m); err != nil {
		t.Fatal(err)
	}
	if m.Type() != "exact" || m.Value() != "exact" {
		t.Errorf("got type=%s val=%s, want exact/exact", m.Type(), m.Value())
	}
}

func TestMatchUnmarshalObject(t *testing.T) {
	tests := []struct {
		in  string
		typ string
		val string
	}{
		{`{"exact": "/foo"}`, "exact", "/foo"},
		{`{"prefix": "/api/"}`, "prefix", "/api/"},
		{`{"suffix": ".ico"}`, "suffix", ".ico"},
	}
	for _, tc := range tests {
		var m Match
		if err := json.Unmarshal([]byte(tc.in), &m); err != nil {
			t.Errorf("unmarshal %s: %v", tc.in, err)
			continue
		}
		if m.Type() != tc.typ || m.Value() != tc.val {
			t.Errorf("%s: got type=%s val=%s, want %s/%s", tc.in, m.Type(), m.Value(), tc.typ, tc.val)
		}
	}
}

func TestMatchUnmarshalEmptyObject(t *testing.T) {
	var m Match
	if err := json.Unmarshal([]byte(`{}`), &m); err != nil {
		t.Fatal(err)
	}
	if !m.IsZero() {
		t.Error("empty object should be zero (wildcard)")
	}
}

func TestMatchMarshalRoundtrip(t *testing.T) {
	cases := []string{`"foo"`, `{"prefix":"/api/"}`, `{"suffix":".ico"}`, `{}`}
	for _, in := range cases {
		var m Match
		if err := json.Unmarshal([]byte(in), &m); err != nil {
			t.Errorf("unmarshal %s: %v", in, err)
			continue
		}
		out, err := json.Marshal(m)
		if err != nil {
			t.Errorf("marshal %s: %v", in, err)
			continue
		}
		var m2 Match
		if err := json.Unmarshal(out, &m2); err != nil {
			t.Errorf("re-unmarshal %s: %v", out, err)
			continue
		}
		if m.Type() != m2.Type() || m.Value() != m2.Value() {
			t.Errorf("roundtrip mismatch: %s → %s → type=%s val=%s", in, out, m2.Type(), m2.Value())
		}
	}
}

func TestMatchMatches(t *testing.T) {
	tests := []struct {
		typ, val, input string
		want            bool
	}{
		{"exact", "/foo", "/foo", true},
		{"exact", "/foo", "/bar", false},
		{"prefix", "/api/", "/api/v1/data", true},
		{"prefix", "/api/", "/other/data", false},
		{"suffix", ".ico", "/favicon.ico", true},
		{"suffix", ".ico", "/style.css", false},
		{"", "", "anything", true},
	}
	for _, tc := range tests {
		m := Match{kind: matchKindFromString(tc.typ), val: tc.val}
		if got := m.Matches(tc.input); got != tc.want {
			t.Errorf("%s(%q).Matches(%q) = %v, want %v", tc.typ, tc.val, tc.input, got, tc.want)
		}
	}
}

func matchKindFromString(s string) matchKind {
	switch s {
	case "exact":
		return matchExact
	case "prefix":
		return matchPrefix
	case "suffix":
		return matchSuffix
	default:
		return matchNone
	}
}

func TestEffectiveIgnoreParams_WildcardRule(t *testing.T) {
	k := matchNone
	cfg := MatchConfig{
		{Match: MatchFields{}, IgnoreQueryParams: []string{"ts", "ver"}},
	}
	_ = k
	got := EffectiveIgnoreParams("http://any.host.com/any/path?ts=1&ver=2&keep=3", &cfg)
	want := []string{"ts", "ver"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wildcard rule: got %v, want %v", got, want)
	}
}

func TestEffectiveIgnoreParams_HostRule(t *testing.T) {
	cfg := MatchConfig{
		{
			Match: MatchFields{
				Host: &Match{kind: matchExact, val: "live.example.com"},
				Path: &Match{kind: matchPrefix, val: "/hls/"},
			},
			IgnoreQueryParams: []string{"uid", "sid"},
		},
	}
	got := EffectiveIgnoreParams("http://live.example.com/hls/master.m3u8?uid=1&sid=2&keep=3", &cfg)
	want := []string{"uid", "sid"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("host rule: got %v, want %v", got, want)
	}
}

func TestEffectiveIgnoreParams_NoMatch(t *testing.T) {
	cfg := MatchConfig{
		{
			Match:             MatchFields{Host: &Match{kind: matchExact, val: "live.example.com"}},
			IgnoreQueryParams: []string{"uid"},
		},
	}
	got := EffectiveIgnoreParams("http://other.host.com/path?uid=1", &cfg)
	if len(got) != 0 {
		t.Errorf("no match should return empty, got %v", got)
	}
}

func TestEffectiveIgnoreParams_HostCaseInsensitive(t *testing.T) {
	cfg := MatchConfig{
		{
			Match:             MatchFields{Host: &Match{kind: matchExact, val: "Live.Example.Com"}},
			IgnoreQueryParams: []string{"uid"},
		},
	}
	got := EffectiveIgnoreParams("http://live.example.com/?uid=1", &cfg)
	want := []string{"uid"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("case insensitive: got %v, want %v", got, want)
	}
}

func TestEffectiveIgnoreParams_MultipleRules(t *testing.T) {
	cfg := MatchConfig{
		{
			Match: MatchFields{
				Host: &Match{kind: matchExact, val: "live.example.com"},
				Path: &Match{kind: matchPrefix, val: "/hls/"},
			},
			IgnoreQueryParams: []string{"uid"},
		},
		{
			Match: MatchFields{
				Host: &Match{kind: matchExact, val: "live.example.com"},
				Path: &Match{kind: matchPrefix, val: "/dash/"},
			},
			IgnoreQueryParams: []string{"sid"},
		},
	}
	got := EffectiveIgnoreParams("http://live.example.com/hls/master.m3u8?uid=1", &cfg)
	want := []string{"uid"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("multiple rules, only first matches: got %v, want %v", got, want)
	}
}

func TestEffectiveIgnoreParams_NilConfig(t *testing.T) {
	got := EffectiveIgnoreParams("http://any.host.com/", nil)
	if len(got) != 0 {
		t.Errorf("nil config should return nil, got %v", got)
	}
}

func TestIsIgnored_BasicHost(t *testing.T) {
	cfg := MatchConfig{
		{Match: MatchFields{Host: &Match{kind: matchExact, val: "ads.example.com"}}, Ignore: true},
	}
	if !IsIgnored("http://ads.example.com/track", &cfg) {
		t.Error("expected ignored")
	}
	if IsIgnored("http://content.example.com/page", &cfg) {
		t.Error("should not be ignored")
	}
}

func TestIsIgnored_HostWithPath(t *testing.T) {
	cfg := MatchConfig{
		{Match: MatchFields{Host: &Match{kind: matchExact, val: "www.iq.com"}, Path: &Match{kind: matchExact, val: "/favicon.ico"}}, Ignore: true},
	}
	if !IsIgnored("http://www.iq.com/favicon.ico", &cfg) {
		t.Error("expected ignored (host+path match)")
	}
	if IsIgnored("http://www.iq.com/content/show", &cfg) {
		t.Error("should not be ignored (path differs)")
	}
}

func TestIsIgnored_PathPrefix(t *testing.T) {
	cfg := MatchConfig{
		{Match: MatchFields{Host: &Match{kind: matchExact, val: "www.iq.com"}, Path: &Match{kind: matchPrefix, val: "/resources/"}}, Ignore: true},
	}
	if !IsIgnored("http://www.iq.com/resources/collect", &cfg) {
		t.Error("expected ignored (path prefix)")
	}
	if IsIgnored("http://www.iq.com/content/show", &cfg) {
		t.Error("should not be ignored")
	}
}

func TestIsIgnored_NilConfig(t *testing.T) {
	if IsIgnored("http://ads.example.com/track", nil) {
		t.Error("nil config should not ignore")
	}
}

func TestIsIgnored_EmptyConfig(t *testing.T) {
	cfg := MatchConfig{}
	if IsIgnored("http://ads.example.com/track", &cfg) {
		t.Error("empty config should not ignore")
	}
}

func TestMatchConfig_JSONRoundtrip(t *testing.T) {
	in := `[
		{"match":{"host":{"exact":"analytics.google.com"}},"ignore":true},
		{"match":{"host":{"exact":"www.iq.com"},"path":{"exact":"/favicon.ico"}},"ignore":true},
		{"match":{"host":{"exact":"live.example.com"},"path":{"prefix":"/api/"}},"ignore_query_params":["uid"]}
	]`
	var cfg MatchConfig
	if err := json.Unmarshal([]byte(in), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(cfg))
	}
	if !cfg[0].Ignore || cfg[0].Match.Host == nil || cfg[0].Match.Host.Value() != "analytics.google.com" {
		t.Error("rule 0 mismatch")
	}
	if !cfg[1].Ignore || cfg[1].Match.Path == nil || cfg[1].Match.Path.Value() != "/favicon.ico" {
		t.Error("rule 1 mismatch")
	}
	if cfg[2].Ignore || len(cfg[2].IgnoreQueryParams) != 1 || cfg[2].IgnoreQueryParams[0] != "uid" {
		t.Error("rule 2 mismatch")
	}

	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var cfg2 MatchConfig
	if err := json.Unmarshal(out, &cfg2); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if !reflect.DeepEqual(cfg, cfg2) {
		t.Errorf("roundtrip mismatch:\n  in:  %s\n  out: %s", in, out)
	}
}

func TestMatchConfig_JSONShorthand(t *testing.T) {
	in := `[
		{"match":{"host":"analytics.google.com"},"ignore":true},
		{"match":{"host":"www.iq.com","path":"/favicon.ico"},"ignore":true}
	]`
	var cfg MatchConfig
	if err := json.Unmarshal([]byte(in), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(cfg))
	}
	if cfg[0].Match.Host == nil || cfg[0].Match.Host.Type() != "exact" {
		t.Error("rule 0 host should be exact")
	}
	if cfg[1].Match.Path == nil || cfg[1].Match.Path.Value() != "/favicon.ico" {
		t.Error("rule 1 path should be /favicon.ico")
	}
}

func TestLoadMatchConfig(t *testing.T) {
	dir := t.TempDir()
	data := `[{"match":{"host":"example.com"},"ignore":true}]`
	if err := os.WriteFile(filepath.Join(dir, "match-config.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadMatchConfig(filepath.Join(dir, "match-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || len(*cfg) != 1 {
		t.Errorf("expected 1 rule, got %v", cfg)
	}
}

func TestLoadMatchConfig_Missing(t *testing.T) {
	_, err := LoadMatchConfig(t.TempDir() + "/nonexistent.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}
