package rules

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	path  string
	mu    sync.Mutex
	rules []*Rule
}

func NewStore(path string) *Store {
	return &Store{
		path:  path,
		rules: make([]*Rule, 0),
	}
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read rules file: %w", err)
	}

	return json.Unmarshal(data, &s.rules)
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s.rules, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal rules: %w", err)
	}

	return os.WriteFile(s.path, data, 0644)
}

func (s *Store) GetRules() []*Rule {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*Rule, len(s.rules))
	copy(result, s.rules)
	return result
}

func (s *Store) AddRule(rule *Rule) error {
	s.mu.Lock()
	s.rules = append(s.rules, rule)
	s.mu.Unlock()
	return s.Save()
}

func (s *Store) RemoveRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, rule := range s.rules {
		if rule.ID == id {
			s.rules = append(s.rules[:i], s.rules[i+1:]...)
			return s.Save()
		}
	}
	return fmt.Errorf("rule %s not found", id)
}

func (s *Store) UpdateRule(rule *Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, r := range s.rules {
		if r.ID == rule.ID {
			s.rules[i] = rule
			return s.Save()
		}
	}
	return fmt.Errorf("rule %s not found", rule.ID)
}
