package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Entry struct {
	ID        string          `json:"id"`
	Timestamp time.Time       `json:"timestamp"`
	Request   RequestRecord   `json:"request"`
	Response  *ResponseRecord `json:"response,omitempty"`
	Action    string          `json:"action"`
	Modified  bool            `json:"modified"`
}

type RequestRecord struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Host    string              `json:"host"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body,omitempty"`
}

type ResponseRecord struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body,omitempty"`
}

type Store struct {
	dir     string
	mu      sync.Mutex
	entries []*Entry
}

type ListEntry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Method    string    `json:"method"`
	URL       string    `json:"url"`
	Host      string    `json:"host"`
	Status    *int      `json:"status,omitempty"`
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create history dir: %w", err)
	}

	s := &Store{
		dir:     dir,
		entries: make([]*Entry, 0),
	}

	if err := s.loadAll(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Store) loadAll() error {
	pattern := filepath.Join(s.dir, "*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("glob history: %w", err)
	}

	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var entry Entry
		if err := json.Unmarshal(data, &entry); err != nil {
			continue
		}

		s.entries = append(s.entries, &entry)
	}

	sort.Slice(s.entries, func(i, j int) bool {
		return s.entries[i].Timestamp.After(s.entries[j].Timestamp)
	})

	return nil
}

func (s *Store) Save(entry *Entry) error {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}

	path := filepath.Join(s.dir, entry.ID+".json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}

	s.mu.Lock()
	s.entries = append([]*Entry{entry}, s.entries...)
	sort.Slice(s.entries, func(i, j int) bool {
		return s.entries[i].Timestamp.After(s.entries[j].Timestamp)
	})
	s.mu.Unlock()

	return nil
}

func (s *Store) List() []*Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*Entry, len(s.entries))
	copy(result, s.entries)
	return result
}

func (s *Store) ListSummary() []*ListEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*ListEntry, 0, len(s.entries))
	for _, e := range s.entries {
		le := &ListEntry{
			ID:        e.ID,
			Timestamp: e.Timestamp,
			Method:    e.Request.Method,
			URL:       e.Request.URL,
			Host:      e.Request.Host,
		}
		if e.Response != nil {
			le.Status = &e.Response.Status
		}
		result = append(result, le)
	}
	return result
}

func (s *Store) ListSince(since time.Time) []*ListEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*ListEntry, 0)
	for _, e := range s.entries {
		if e.Timestamp.After(since) {
			le := &ListEntry{
				ID:        e.ID,
				Timestamp: e.Timestamp,
				Method:    e.Request.Method,
				URL:       e.Request.URL,
				Host:      e.Request.Host,
			}
			if e.Response != nil {
				le.Status = &e.Response.Status
			}
			result = append(result, le)
		}
	}
	return result
}

func (s *Store) Get(id string) (*Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, e := range s.entries {
		if e.ID == id {
			return e, nil
		}
	}

	return nil, fmt.Errorf("entry %s not found", id)
}

func (s *Store) Update(entry *Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}

	path := filepath.Join(s.dir, entry.ID+".json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}

	return nil
}

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	pattern := filepath.Join(s.dir, "*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}

	for _, path := range matches {
		if err := os.Remove(path); err != nil {
			return err
		}
	}

	s.entries = s.entries[:0]
	return nil
}
