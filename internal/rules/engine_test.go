package rules

import (
	"testing"
)

func TestEngine_Match_Method(t *testing.T) {
	engine := NewEngine()
	engine.Load([]*Rule{
		{
			ID:      "1",
			Name:    "GET only",
			Match:   MatchRule{Method: "GET"},
			Action:  ActionMock,
			Enabled: true,
		},
	})

	tests := []struct {
		method string
		match  bool
	}{
		{"GET", true},
		{"POST", false},
		{"PUT", false},
	}

	for _, tt := range tests {
		rule := engine.Match(tt.method, "example.com", "http://example.com/api", nil)
		if tt.match && rule == nil {
			t.Errorf("Match(%s) = nil, want rule", tt.method)
		}
		if !tt.match && rule != nil {
			t.Errorf("Match(%s) = %v, want nil", tt.method, rule)
		}
	}
}

func TestEngine_Match_URLPattern(t *testing.T) {
	engine := NewEngine()
	engine.Load([]*Rule{
		{
			ID:      "1",
			Name:    "API mock",
			Match:   MatchRule{URLPattern: ".*/api/info$"},
			Action:  ActionMock,
			Enabled: true,
		},
	})

	tests := []struct {
		url   string
		match bool
	}{
		{"http://example.com/api/info", true},
		{"http://example.com/api/info?version=1", false},
		{"http://example.com/api/users", false},
		{"http://example.com/other/info", false},
	}

	for _, tt := range tests {
		rule := engine.Match("GET", "example.com", tt.url, nil)
		if tt.match && rule == nil {
			t.Errorf("Match(%s) = nil, want rule", tt.url)
		}
		if !tt.match && rule != nil {
			t.Errorf("Match(%s) = %v, want nil", tt.url, rule)
		}
	}
}

func TestEngine_Match_Host(t *testing.T) {
	engine := NewEngine()
	engine.Load([]*Rule{
		{
			ID:      "1",
			Name:    "Block ads",
			Match:   MatchRule{Host: "ads.example.com"},
			Action:  ActionPassthrough,
			Enabled: true,
		},
	})

	tests := []struct {
		host  string
		match bool
	}{
		{"ads.example.com:443", true},
		{"ads.example.com", true},
		{"tracking.example.com", false},
		{"example.com", false},
	}

	for _, tt := range tests {
		rule := engine.Match("GET", tt.host, "http://"+tt.host+"/", nil)
		if tt.match && rule == nil {
			t.Errorf("Match(%s) = nil, want rule", tt.host)
		}
		if !tt.match && rule != nil {
			t.Errorf("Match(%s) = %v, want nil", tt.host, rule)
		}
	}
}

func TestEngine_Match_Headers(t *testing.T) {
	engine := NewEngine()
	engine.Load([]*Rule{
		{
			ID:      "1",
			Name:    "JSON API",
			Match:   MatchRule{Headers: map[string]string{"Content-Type": "application/json"}},
			Action:  ActionPassthrough,
			Enabled: true,
		},
	})

	headers := map[string][]string{
		"Content-Type": {"application/json; charset=utf-8"},
	}

	rule := engine.Match("POST", "example.com", "http://example.com/api", headers)
	if rule == nil {
		t.Error("Match() with JSON headers = nil, want rule")
	}

	headers["Content-Type"] = []string{"text/html"}
	rule = engine.Match("POST", "example.com", "http://example.com/api", headers)
	if rule != nil {
		t.Error("Match() with HTML headers = rule, want nil")
	}
}

func TestEngine_DisabledRule(t *testing.T) {
	engine := NewEngine()
	engine.Load([]*Rule{
		{
			ID:      "1",
			Name:    "Disabled",
			Match:   MatchRule{Method: "GET"},
			Action:  ActionMock,
			Enabled: false,
		},
	})

	rule := engine.Match("GET", "example.com", "http://example.com/", nil)
	if rule != nil {
		t.Error("Match() with disabled rule = rule, want nil")
	}
}

func TestEngine_NoMatch(t *testing.T) {
	engine := NewEngine()
	engine.Load([]*Rule{
		{
			ID:      "1",
			Name:    "POST only",
			Match:   MatchRule{Method: "POST"},
			Action:  ActionMock,
			Enabled: true,
		},
	})

	rule := engine.Match("GET", "example.com", "http://example.com/", nil)
	if rule != nil {
		t.Error("Match() with no matching rule = rule, want nil")
	}
}

func TestEngine_EmptyRules(t *testing.T) {
	engine := NewEngine()

	rule := engine.Match("GET", "example.com", "http://example.com/", nil)
	if rule != nil {
		t.Error("Match() with empty rules = rule, want nil")
	}
}

func TestEngine_AddRemoveRule(t *testing.T) {
	engine := NewEngine()

	rule := &Rule{
		ID:      "new",
		Name:    "New rule",
		Match:   MatchRule{Method: "DELETE"},
		Action:  ActionMock,
		Enabled: true,
	}

	engine.AddRule(rule)

	rules := engine.GetRules()
	if len(rules) != 1 {
		t.Errorf("GetRules() after add = %d rules, want 1", len(rules))
	}

	removed := engine.RemoveRule("new")
	if !removed {
		t.Error("RemoveRule() = false, want true")
	}

	rules = engine.GetRules()
	if len(rules) != 0 {
		t.Errorf("GetRules() after remove = %d rules, want 0", len(rules))
	}
}

func TestEngine_RemoveNonexistent(t *testing.T) {
	engine := NewEngine()

	removed := engine.RemoveRule("nonexistent")
	if removed {
		t.Error("RemoveRule() nonexistent = true, want false")
	}
}

func TestEngine_CaseInsensitiveMethod(t *testing.T) {
	engine := NewEngine()
	engine.Load([]*Rule{
		{
			ID:      "1",
			Name:    "get rule",
			Match:   MatchRule{Method: "GET"},
			Action:  ActionPassthrough,
			Enabled: true,
		},
	})

	rule := engine.Match("get", "example.com", "http://example.com/", nil)
	if rule == nil {
		t.Error("Match() case-insensitive = nil, want rule")
	}
}

func TestEngine_MultipleRules_FirstMatchWins(t *testing.T) {
	engine := NewEngine()
	engine.Load([]*Rule{
		{
			ID:      "1",
			Name:    "First",
			Match:   MatchRule{Method: "GET"},
			Action:  ActionPassthrough,
			Enabled: true,
		},
		{
			ID:      "2",
			Name:    "Second",
			Match:   MatchRule{Method: "GET"},
			Action:  ActionMock,
			Enabled: true,
		},
	})

	rule := engine.Match("GET", "example.com", "http://example.com/", nil)
	if rule == nil {
		t.Error("Match() = nil, want rule")
	}
	if rule.ID != "1" {
		t.Errorf("Match() = rule %q, want %q", rule.ID, "1")
	}
}
