package session

import (
	"reflect"
	"testing"
)

func TestEffectiveIgnoreParams_GlobalOnly(t *testing.T) {
	cfg := &MatchConfig{IgnoreQueryParams: []string{"ts", "ver"}}
	got := cfg.EffectiveIgnoreParams("any.host.com", "/any/path")
	want := []string{"ts", "ver"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("global only: got %v, want %v", got, want)
	}
}

func TestEffectiveIgnoreParams_HostRuleOnly(t *testing.T) {
	cfg := &MatchConfig{HostRules: []HostRule{
		{Host: "live.example.com", PathPrefix: "/hls/", IgnoreQueryParams: []string{"uid", "sid"}},
	}}
	got := cfg.EffectiveIgnoreParams("live.example.com", "/hls/master.m3u8")
	want := []string{"uid", "sid"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("host rule only: got %v, want %v", got, want)
	}
}

func TestEffectiveIgnoreParams_Merge(t *testing.T) {
	cfg := &MatchConfig{
		IgnoreQueryParams: []string{"ts", "ver"},
		HostRules: []HostRule{
			{Host: "live.example.com", PathPrefix: "/hls/", IgnoreQueryParams: []string{"uid", "ts"}},
		},
	}
	got := cfg.EffectiveIgnoreParams("live.example.com", "/hls/master.m3u8")
	want := []string{"ts", "ver", "uid"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merge: got %v, want %v", got, want)
	}
}

func TestEffectiveIgnoreParams_NoMatch(t *testing.T) {
	cfg := &MatchConfig{
		HostRules: []HostRule{
			{Host: "live.example.com", PathPrefix: "/hls/", IgnoreQueryParams: []string{"uid"}},
		},
	}
	got := cfg.EffectiveIgnoreParams("other.host.com", "/hls/master.m3u8")
	if len(got) != 0 {
		t.Errorf("no match should return empty, got %v", got)
	}
}

func TestEffectiveIgnoreParams_PathPrefixMismatch(t *testing.T) {
	cfg := &MatchConfig{
		HostRules: []HostRule{
			{Host: "live.example.com", PathPrefix: "/hls/", IgnoreQueryParams: []string{"uid"}},
		},
	}
	got := cfg.EffectiveIgnoreParams("live.example.com", "/dash/manifest.mpd")
	if len(got) != 0 {
		t.Errorf("path prefix mismatch should return empty, got %v", got)
	}
}

func TestEffectiveIgnoreParams_HostCaseInsensitive(t *testing.T) {
	cfg := &MatchConfig{
		HostRules: []HostRule{
			{Host: "Live.Example.Com", IgnoreQueryParams: []string{"uid"}},
		},
	}
	got := cfg.EffectiveIgnoreParams("live.example.com", "/")
	want := []string{"uid"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("case insensitive host: got %v, want %v", got, want)
	}
}

func TestEffectiveIgnoreParams_MultipleRulesSameHost(t *testing.T) {
	cfg := &MatchConfig{
		HostRules: []HostRule{
			{Host: "live.example.com", PathPrefix: "/hls/", IgnoreQueryParams: []string{"uid"}},
			{Host: "live.example.com", PathPrefix: "/dash/", IgnoreQueryParams: []string{"sid"}},
		},
	}
	got := cfg.EffectiveIgnoreParams("live.example.com", "/hls/master.m3u8")
	want := []string{"uid"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("multiple rules, path matches first: got %v, want %v", got, want)
	}
}

func TestEffectiveIgnoreParams_NilConfig(t *testing.T) {
	var cfg *MatchConfig
	got := cfg.EffectiveIgnoreParams("any.host.com", "/")
	if len(got) != 0 {
		t.Errorf("nil config should return nil, got %v", got)
	}
}
