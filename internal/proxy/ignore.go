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

type IgnoreStore struct {
	path     string
	mu       sync.RWMutex
	hosts    []string
	compiled map[string]*regexp.Regexp
}

func NewIgnoreStore(path string) *IgnoreStore {
	return &IgnoreStore{path: path, compiled: make(map[string]*regexp.Regexp)}
}

func (s *IgnoreStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.hosts = []string{}
			return nil
		}
		return fmt.Errorf("read ignore file: %w", err)
	}

	var hosts []string
	if err := json.Unmarshal(data, &hosts); err != nil {
		return fmt.Errorf("unmarshal ignore file: %w", err)
	}

	s.hosts = hosts
	s.rebuildCompiled()
	return nil
}

func (s *IgnoreStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(s.hosts, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ignore list: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0644); err != nil {
		return fmt.Errorf("write ignore file: %w", err)
	}

	return nil
}

func (s *IgnoreStore) Add(host string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, h := range s.hosts {
		if h == host {
			return nil
		}
	}

	s.hosts = append(s.hosts, host)
	sort.Strings(s.hosts)
	if strings.Contains(host, "*") {
		s.compiled[host] = s.compilePattern(host)
	}
	s.mu.Unlock()
	defer s.mu.Lock()

	return s.saveLocked()
}

func (s *IgnoreStore) Remove(host string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, h := range s.hosts {
		if h == host {
			s.hosts = append(s.hosts[:i], s.hosts[i+1:]...)
			delete(s.compiled, host)
			break
		}
	}

	return s.saveLocked()
}

func (s *IgnoreStore) IsIgnored(host string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, h := range s.hosts {
		if h == host {
			return true
		}
	}
	return false
}

func (s *IgnoreStore) Matches(host string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.hosts) == 0 {
		return false
	}
	for _, pattern := range s.hosts {
		if pattern == host {
			return true
		}
		if re, ok := s.compiled[pattern]; ok {
			if re.MatchString(host) {
				return true
			}
		}
	}
	return false
}

func (s *IgnoreStore) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]string, len(s.hosts))
	copy(result, s.hosts)
	return result
}

func (s *IgnoreStore) compilePattern(pattern string) *regexp.Regexp {
	regex := regexp.QuoteMeta(pattern)
	regex = strings.ReplaceAll(regex, "\\*", ".*")
	return regexp.MustCompile("^" + regex + "$")
}

func (s *IgnoreStore) rebuildCompiled() {
	s.compiled = make(map[string]*regexp.Regexp, len(s.hosts))
	for _, h := range s.hosts {
		if strings.Contains(h, "*") {
			s.compiled[h] = s.compilePattern(h)
		}
	}
}

func (s *IgnoreStore) saveLocked() error {
	data, err := json.MarshalIndent(s.hosts, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ignore list: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0644); err != nil {
		return fmt.Errorf("write ignore file: %w", err)
	}

	return nil
}
