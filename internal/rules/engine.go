package rules

import (
	"regexp"
	"strings"
)

type Engine struct {
	rules []*Rule
}

func NewEngine() *Engine {
	return &Engine{
		rules: make([]*Rule, 0),
	}
}

func (e *Engine) Load(rules []*Rule) {
	e.rules = rules
}

func (e *Engine) Match(method, host, url string, headers map[string][]string) *Rule {
	for _, rule := range e.rules {
		if !rule.Enabled {
			continue
		}
		if e.matchesRule(rule, method, host, url, headers) {
			return rule
		}
	}
	return nil
}

func (e *Engine) matchesRule(rule *Rule, method, host, url string, headers map[string][]string) bool {
	if rule.Match.Method != "" && !strings.EqualFold(rule.Match.Method, method) {
		return false
	}

	if rule.Match.Host != "" && !strings.Contains(host, rule.Match.Host) {
		return false
	}

	if rule.Match.URLPattern != "" {
		matched, err := regexp.MatchString(rule.Match.URLPattern, url)
		if err != nil || !matched {
			return false
		}
	}

	for key, value := range rule.Match.Headers {
		found := false
		for _, v := range headers[key] {
			if strings.Contains(v, value) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

func (e *Engine) AddRule(rule *Rule) {
	e.rules = append(e.rules, rule)
}

func (e *Engine) RemoveRule(id string) bool {
	for i, rule := range e.rules {
		if rule.ID == id {
			e.rules = append(e.rules[:i], e.rules[i+1:]...)
			return true
		}
	}
	return false
}

func (e *Engine) GetRules() []*Rule {
	result := make([]*Rule, len(e.rules))
	copy(result, e.rules)
	return result
}
