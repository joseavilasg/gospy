package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"gospy/internal/logging"

	"github.com/google/uuid"
)

type Entry struct {
	ID                string          `json:"id"`
	Timestamp         time.Time       `json:"timestamp"`
	Request           RequestRecord   `json:"request"`
	Response          *ResponseRecord `json:"response,omitempty"`
	ServerRequest     *RequestRecord  `json:"serverRequest,omitempty"`
	ServerResponse    *ResponseRecord `json:"serverResponse,omitempty"`
	AppliedAction     string          `json:"appliedAction,omitempty"`
	RuleName          string          `json:"ruleName,omitempty"`
	ReplayedFrom      string          `json:"replayedFrom,omitempty"`
	ClientProcess     string          `json:"clientProcess,omitempty"`
	ClientPID         uint32          `json:"clientPid,omitempty"`
	ClientPath        string          `json:"clientPath,omitempty"`
	ClientDisplayName string          `json:"clientDisplayName,omitempty"`
}

type RequestRecord struct {
	Method        string              `json:"method"`
	URL           string              `json:"url"`
	Host          string              `json:"host"`
	Headers       map[string][]string `json:"headers"`
	EditedHeaders map[string][]string `json:"editedHeaders,omitempty"`
	Body          string              `json:"body,omitempty"`
	RawBody       string              `json:"rawBody,omitempty"`
	Compression   string              `json:"compression,omitempty"`
	EditedBody    string              `json:"editedBody,omitempty"`
}

type ResponseRecord struct {
	Status      int                 `json:"status"`
	Headers     map[string][]string `json:"headers"`
	Body        string              `json:"body,omitempty"`
	RawBody     string              `json:"rawBody,omitempty"`
	Compression string              `json:"compression,omitempty"`
	EditedBody  string              `json:"editedBody,omitempty"`
}

type Store struct {
	dir     string
	mu      sync.Mutex
	index   []*ListEntry
	pending []*Entry
}

type ListEntry struct {
	ID                string    `json:"id"`
	Timestamp         time.Time `json:"timestamp"`
	UpdatedAt         time.Time `json:"updatedAt"`
	Method            string    `json:"method"`
	URL               string    `json:"url"`
	Host              string    `json:"host"`
	Status            *int      `json:"status,omitempty"`
	ReplayedFrom      string    `json:"replayedFrom,omitempty"`
	AppliedAction     string    `json:"appliedAction,omitempty"`
	RuleName          string    `json:"ruleName,omitempty"`
	ClientProcess     string    `json:"clientProcess,omitempty"`
	ClientPID         uint32    `json:"clientPid,omitempty"`
	ClientDisplayName string    `json:"clientDisplayName,omitempty"`
	Referer           string    `json:"referer,omitempty"`
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
			logging.Log.Warn("index.json not found, rebuilding index")
			return s.buildIndex()
		}
		return fmt.Errorf("read index: %w", err)
	}

	var index []*ListEntry
	if err := json.Unmarshal(data, &index); err != nil {
		logging.Log.Error("index.json is corrupt, rebuilding index")
		return s.buildIndex()
	}

	s.index = index
	return nil
}

func skipJSONValue(dec *json.Decoder) error {
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
			if depth == 0 {
				return nil
			}
		default:
			if depth == 0 {
				return nil
			}
		}
	}
}

func (s *Store) buildIndex() error {
	pattern := filepath.Join(s.dir, "*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("glob history: %w", err)
	}

	for _, path := range matches {
		file, err := os.Open(path)
		if err != nil {
			continue
		}

		dec := json.NewDecoder(file)
		if _, err := dec.Token(); err != nil {
			file.Close()
			continue
		}

		le := &ListEntry{}
		for dec.More() {
			key, _ := dec.Token()
			k, ok := key.(string)
			if !ok {
				skipJSONValue(dec)
				continue
			}
			switch k {
			case "id":
				dec.Decode(&le.ID)
			case "timestamp":
				var ts string
				dec.Decode(&ts)
				if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
					le.Timestamp = t
					le.UpdatedAt = t
				}
			case "replayedFrom":
				dec.Decode(&le.ReplayedFrom)
			case "appliedAction":
				dec.Decode(&le.AppliedAction)
			case "ruleName":
				dec.Decode(&le.RuleName)
			case "clientProcess":
				dec.Decode(&le.ClientProcess)
			case "clientPid":
				dec.Decode(&le.ClientPID)
			case "clientDisplayName":
				dec.Decode(&le.ClientDisplayName)
			case "request":
				s.decodeRequestHeader(dec, le)
			case "response":
				s.decodeResponseStatus(dec, le)
			default:
				skipJSONValue(dec)
			}
		}
		file.Close()
		s.index = append(s.index, le)
	}

	sort.Slice(s.index, func(i, j int) bool {
		return s.index[i].Timestamp.After(s.index[j].Timestamp)
	})

	return s.persistIndex()
}

func (s *Store) decodeRequestHeader(dec *json.Decoder, le *ListEntry) {
	if _, err := dec.Token(); err != nil {
		return
	}
	for dec.More() {
		key, _ := dec.Token()
		k, ok := key.(string)
		if !ok {
			skipJSONValue(dec)
			continue
		}
		switch k {
		case "method":
			dec.Decode(&le.Method)
		case "url":
			dec.Decode(&le.URL)
		case "host":
			dec.Decode(&le.Host)
		case "headers":
			var headers map[string][]string
			dec.Decode(&headers)
			if refs, ok := headers["Referer"]; ok && len(refs) > 0 {
				le.Referer = refs[0]
			}
		default:
			skipJSONValue(dec)
		}
	}
	dec.Token()
}

func (s *Store) decodeResponseStatus(dec *json.Decoder, le *ListEntry) {
	if _, err := dec.Token(); err != nil {
		return
	}
	for dec.More() {
		key, _ := dec.Token()
		k, ok := key.(string)
		if !ok {
			skipJSONValue(dec)
			continue
		}
		if k == "status" {
			var status int
			dec.Decode(&status)
			le.Status = &status
		} else {
			skipJSONValue(dec)
		}
	}
	dec.Token()
}

func (s *Store) persistIndex() error {
	data, err := json.Marshal(s.index)
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}
	tmp := s.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write index tmp: %w", err)
	}
	return os.Rename(tmp, s.indexPath())
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
		ID:                entry.ID,
		Timestamp:         entry.Timestamp,
		UpdatedAt:         entry.Timestamp,
		Method:            entry.Request.Method,
		URL:               entry.Request.URL,
		Host:              entry.Request.Host,
		ReplayedFrom:      entry.ReplayedFrom,
		AppliedAction:     entry.AppliedAction,
		RuleName:          entry.RuleName,
		ClientProcess:     entry.ClientProcess,
		ClientDisplayName: entry.ClientDisplayName,
	}
	if refs, ok := entry.Request.Headers["Referer"]; ok && len(refs) > 0 {
		le.Referer = refs[0]
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
		if le.UpdatedAt.After(since) {
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
			le.UpdatedAt = time.Now()
			break
		}
	}
	err = s.persistIndex()
	s.mu.Unlock()

	return err
}

func (s *Store) SaveEditedBody(id, target, body string) error {
	entry, err := s.Get(id)
	if err != nil {
		return err
	}

	if target == "request" {
		entry.Request.EditedBody = body
	} else {
		if entry.Response == nil {
			return fmt.Errorf("no response to edit")
		}
		entry.Response.EditedBody = body
	}

	return s.Update(entry)
}

func (s *Store) RevertBody(id, target string) error {
	entry, err := s.Get(id)
	if err != nil {
		return err
	}

	if target == "request" {
		entry.Request.EditedBody = ""
	} else {
		if entry.Response == nil {
			return fmt.Errorf("no response to revert")
		}
		entry.Response.EditedBody = ""
	}

	return s.Update(entry)
}

func (s *Store) SaveEditedHeaders(id string, headers map[string][]string) error {
	entry, err := s.Get(id)
	if err != nil {
		return err
	}
	entry.Request.EditedHeaders = headers
	return s.Update(entry)
}

func (s *Store) RevertHeaders(id string) error {
	entry, err := s.Get(id)
	if err != nil {
		return err
	}
	entry.Request.EditedHeaders = nil
	return s.Update(entry)
}

func (s *Store) Replay(id string, modifiedBody string) (*Entry, error) {
	original, err := s.Get(id)
	if err != nil {
		return nil, err
	}

	headers := original.Request.Headers
	if original.Request.EditedHeaders != nil {
		headers = original.Request.EditedHeaders
	}

	newEntry := &Entry{
		Request: RequestRecord{
			Method:  original.Request.Method,
			URL:     original.Request.URL,
			Host:    original.Request.Host,
			Headers: headers,
			Body:    modifiedBody,
		},
		AppliedAction: "passthrough",
		ReplayedFrom:  original.ID,
	}

	if err := s.Save(newEntry); err != nil {
		return nil, err
	}
	return newEntry, nil
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
