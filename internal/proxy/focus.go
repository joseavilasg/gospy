package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
)

type FocusStore struct {
	path  string
	mu    sync.Mutex
	hosts []string
}

func NewFocusStore(path string) *FocusStore {
	return &FocusStore{path: path}
}

func (s *FocusStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.hosts = []string{}
			return nil
		}
		return fmt.Errorf("read focus file: %w", err)
	}

	var hosts []string
	if err := json.Unmarshal(data, &hosts); err != nil {
		return fmt.Errorf("unmarshal focus file: %w", err)
	}

	s.hosts = hosts
	return nil
}

func (s *FocusStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.saveLocked()
}

func (s *FocusStore) Add(host string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, h := range s.hosts {
		if h == host {
			return nil
		}
	}

	s.hosts = append(s.hosts, host)
	sort.Strings(s.hosts)

	return s.saveLocked()
}

func (s *FocusStore) Remove(host string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, h := range s.hosts {
		if h == host {
			s.hosts = append(s.hosts[:i], s.hosts[i+1:]...)
			break
		}
	}

	return s.saveLocked()
}

func (s *FocusStore) IsFocused(host string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.hosts) == 0 {
		return false
	}
	for _, h := range s.hosts {
		if h == host {
			return true
		}
	}
	return false
}

func (s *FocusStore) Matches(host string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.hosts) == 0 {
		return false
	}
	for _, pattern := range s.hosts {
		if pattern == host {
			return true
		}
		if strings.Contains(pattern, "*") {
			regex := regexp.QuoteMeta(pattern)
			regex = strings.ReplaceAll(regex, "\\*", ".*")
			if matched, _ := regexp.MatchString("^"+regex+"$", host); matched {
				return true
			}
		}
	}
	return false
}

func (s *FocusStore) List() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]string, len(s.hosts))
	copy(result, s.hosts)
	return result
}

func (s *FocusStore) saveLocked() error {
	data, err := json.MarshalIndent(s.hosts, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal focus list: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0644); err != nil {
		return fmt.Errorf("write focus file: %w", err)
	}

	return nil
}
