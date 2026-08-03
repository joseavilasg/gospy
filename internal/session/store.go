package session

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type IndexEntry struct {
	ID        string    `json:"id"`
	Method    string    `json:"method"`
	URL       string    `json:"url"`
	Host      string    `json:"host"`
	Status    int       `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

type Store struct {
	dir          string
	mu           sync.RWMutex
	idx          []*IndexEntry
	byID         map[string]*Entry
	groups       map[string][]*IndexEntry
	cursors      map[string]int
	groupsCfg    *MatchConfig
	groupsIdxLen int
}

func NewOrLoad(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "entries"), 0755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	s := &Store{dir: dir, byID: make(map[string]*Entry)}
	if err := s.loadIndex(); err != nil {
		return s, nil
	}
	return s, nil
}

func (s *Store) loadIndex() error {
	data, err := os.ReadFile(filepath.Join(s.dir, "index.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var entries []*Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	s.idx = make([]*IndexEntry, 0, len(entries))
	for _, e := range entries {
		s.idx = append(s.idx, &IndexEntry{
			ID:        e.ID,
			Method:    e.Request.Method,
			URL:       e.Request.URL,
			Host:      e.Request.Host,
			Status:    e.Response.Status,
			Timestamp: e.Timestamp,
		})
		s.byID[e.ID] = e
	}
	return nil
}

func (s *Store) persistIndex() error {
	entries := make([]*Entry, 0, len(s.idx))
	for _, ie := range s.idx {
		if e, ok := s.byID[ie.ID]; ok {
			entries = append(entries, e)
		}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, "index.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) SaveBin(entryID, suffix string, data []byte) (string, error) {
	filename := entryID + "-" + suffix + ".bin"
	path := filepath.Join(s.dir, "entries", filename)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return filename, nil
}

func (s *Store) SaveEntry(e *Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dir, "entries", e.ID+".json")
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}

	ie := &IndexEntry{
		ID:        e.ID,
		Method:    e.Request.Method,
		URL:       e.Request.URL,
		Host:      e.Request.Host,
		Status:    e.Response.Status,
		Timestamp: e.Timestamp,
	}
	s.idx = append(s.idx, ie)
	s.byID[e.ID] = e

	return s.persistIndex()
}

func (s *Store) Match(method, rawURL string, cfg *MatchConfig) (*Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureGroups(cfg)
	key := matchGroupKey(method, rawURL, cfg)
	group := s.groups[key]
	if len(group) == 0 {
		return nil, false
	}
	idx := s.cursors[key]
	if idx >= len(group) {
		return nil, true
	}
	ie := group[idx]
	s.cursors[key] = idx + 1
	if e, ok := s.byID[ie.ID]; ok {
		return e, false
	}
	return nil, false
}

func matchGroupKey(method, rawURL string, cfg *MatchConfig) string {
	return strings.ToLower(method) + "\x00" + normalizeURL(rawURL, cfg)
}

func (s *Store) ensureGroups(cfg *MatchConfig) {
	if s.groups != nil && s.groupsCfg == cfg && s.groupsIdxLen == len(s.idx) {
		return
	}
	s.groups = make(map[string][]*IndexEntry)
	for _, ie := range s.idx {
		if ie.Status == 0 {
			continue
		}
		key := matchGroupKey(ie.Method, ie.URL, cfg)
		s.groups[key] = append(s.groups[key], ie)
	}
	for key := range s.groups {
		sort.Slice(s.groups[key], func(i, j int) bool {
			return s.groups[key][i].Timestamp.Before(s.groups[key][j].Timestamp)
		})
	}
	s.cursors = make(map[string]int)
	s.groupsCfg = cfg
	s.groupsIdxLen = len(s.idx)
}

func (s *Store) GetEntry(id string) *Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byID[id]
}

func normalizeURL(rawURL string, cfg *MatchConfig) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		u, err = url.Parse("http://" + rawURL)
		if err != nil {
			return rawURL
		}
	}
	if cfg != nil && len(cfg.IgnoreQueryParams) > 0 {
		q := u.Query()
		for _, key := range cfg.IgnoreQueryParams {
			q.Del(key)
		}
		u.RawQuery = q.Encode()
	}
	result := u.Scheme + "://" + strings.ToLower(u.Host) + u.Path
	if u.RawQuery != "" {
		result += "?" + u.RawQuery
	}
	return result
}

func SortByTimestamp(entries []*Entry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})
}
