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
	Method      string              `json:"method"`
	URL         string              `json:"url"`
	Host        string              `json:"host"`
	Headers     map[string][]string `json:"headers"`
	Body        string              `json:"body,omitempty"`
	RawBody     string              `json:"rawBody,omitempty"`
	Compression string              `json:"compression,omitempty"`
}

type ResponseRecord struct {
	Status      int                 `json:"status"`
	Headers     map[string][]string `json:"headers"`
	Body        string              `json:"body,omitempty"`
	RawBody     string              `json:"rawBody,omitempty"`
	Compression string              `json:"compression,omitempty"`
}

type Store struct {
	dir     string
	mu      sync.Mutex
	index   []*ListEntry
	pending []*Entry
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
		index:   make([]*ListEntry, 0),
		pending: make([]*Entry, 0),
	}

	if err := s.loadIndex(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Store) indexPath() string {
	return filepath.Join(s.dir, "index.json")
}

func (s *Store) loadIndex() error {
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return s.buildIndex()
		}
		return fmt.Errorf("read index: %w", err)
	}

	var index []*ListEntry
	if err := json.Unmarshal(data, &index); err != nil {
		return s.buildIndex()
	}

	s.index = index
	return nil
}

func (s *Store) buildIndex() error {
	pattern := filepath.Join(s.dir, "*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("glob history: %w", err)
	}

	type entryHeader struct {
		ID        string `json:"id"`
		Timestamp string `json:"timestamp"`
		Request   struct {
			Method string `json:"method"`
			URL    string `json:"url"`
			Host   string `json:"host"`
		} `json:"request"`
		Response *struct {
			Status int `json:"status"`
		} `json:"response,omitempty"`
	}

	for _, path := range matches {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		decoder := json.NewDecoder(file)
		var h entryHeader
		if err := decoder.Decode(&h); err != nil {
			file.Close()
			continue
		}
		file.Close()

		le := &ListEntry{
			ID:     h.ID,
			Method: h.Request.Method,
			URL:    h.Request.URL,
			Host:   h.Request.Host,
		}
		if t, err := time.Parse(time.RFC3339Nano, h.Timestamp); err == nil {
			le.Timestamp = t
		}
		if h.Response != nil {
			le.Status = &h.Response.Status
		}
		s.index = append(s.index, le)
	}

	sort.Slice(s.index, func(i, j int) bool {
		return s.index[i].Timestamp.After(s.index[j].Timestamp)
	})

	return s.persistIndex()
}

func (s *Store) persistIndex() error {
	data, err := json.Marshal(s.index)
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}
	return os.WriteFile(s.indexPath(), data, 0644)
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

	le := &ListEntry{
		ID:        entry.ID,
		Timestamp: entry.Timestamp,
		Method:    entry.Request.Method,
		URL:       entry.Request.URL,
		Host:      entry.Request.Host,
	}
	if entry.Response != nil {
		le.Status = &entry.Response.Status
	}

	s.mu.Lock()
	if entry.Response == nil {
		s.pending = append(s.pending, entry)
		sort.Slice(s.pending, func(i, j int) bool {
			return s.pending[i].Timestamp.After(s.pending[j].Timestamp)
		})
	}
	s.index = append([]*ListEntry{le}, s.index...)
	sort.Slice(s.index, func(i, j int) bool {
		return s.index[i].Timestamp.After(s.index[j].Timestamp)
	})
	err = s.persistIndex()
	s.mu.Unlock()

	return err
}

func (s *Store) List() []*Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*Entry, len(s.pending))
	copy(result, s.pending)
	return result
}

func (s *Store) ListSummary() []*ListEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*ListEntry, len(s.index))
	copy(result, s.index)
	return result
}

func (s *Store) ListSince(since time.Time) []*ListEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*ListEntry, 0)
	for _, le := range s.index {
		if le.Timestamp.After(since) {
			result = append(result, le)
		}
	}
	return result
}

func (s *Store) Get(id string) (*Entry, error) {
	s.mu.Lock()

	for _, e := range s.pending {
		if e.ID == id {
			result := *e
			s.mu.Unlock()
			return &result, nil
		}
	}
	s.mu.Unlock()

	path := filepath.Join(s.dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("entry %s not found", id)
	}

	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("decode entry: %w", err)
	}

	return &entry, nil
}

func (s *Store) Update(entry *Entry) error {
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}

	path := filepath.Join(s.dir, entry.ID+".json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}

	s.mu.Lock()
	for i, e := range s.pending {
		if e.ID == entry.ID {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			break
		}
	}
	for _, le := range s.index {
		if le.ID == entry.ID {
			if entry.Response != nil {
				le.Status = &entry.Response.Status
			}
			break
		}
	}
	err = s.persistIndex()
	s.mu.Unlock()

	return err
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

	s.index = s.index[:0]
	s.pending = s.pending[:0]
	return s.persistIndex()
}
