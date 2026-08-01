package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
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
	Origin            string          `json:"origin,omitempty"`
	AgentCallID       string          `json:"agentCallId,omitempty"`
}

type ProtobufField struct {
	FieldNumber int             `json:"fieldNumber"`
	WireType    string          `json:"wireType"`
	Value       interface{}     `json:"value,omitempty"`
	ByteOffset  int             `json:"byteOffset"`
	ByteEnd     int             `json:"byteEnd,omitempty"`
	ByteSize    int             `json:"byteSize,omitempty"`
	ZigzagValue interface{}     `json:"zigzagValue,omitempty"`
	SubFields   []ProtobufField `json:"subFields,omitempty"`
}

type MultipartPart struct {
	Name        string `json:"name"`
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Value       string `json:"value,omitempty"`
	IsBinary    bool   `json:"isBinary"`
	Size        int    `json:"size"`
	HexPreview  string `json:"hexPreview,omitempty"`
}

type RequestRecord struct {
	Method          string              `json:"method"`
	URL             string              `json:"url"`
	Host            string              `json:"host"`
	Headers         map[string][]string `json:"headers"`
	EditedHeaders   map[string][]string `json:"editedHeaders,omitempty"`
	Body            string              `json:"body,omitempty"`
	RawBody         string              `json:"rawBody,omitempty"`
	Compression     string              `json:"compression,omitempty"`
	EditedBody      string              `json:"editedBody,omitempty"`
	BodyFile        string              `json:"bodyFile,omitempty"`
	BodySize        int64               `json:"bodySize,omitempty"`
	BodyHex         string              `json:"bodyHex,omitempty"`
	IsBinaryBody    bool                `json:"isBinaryBody,omitempty"`
	ParsedMultipart []MultipartPart     `json:"parsedMultipart,omitempty"`
	ParsedProtobuf  []ProtobufField     `json:"parsedProtobuf,omitempty"`
}

type ResponseRecord struct {
	Status         int                 `json:"status"`
	Headers        map[string][]string `json:"headers"`
	Body           string              `json:"body,omitempty"`
	RawBody        string              `json:"rawBody,omitempty"`
	Compression    string              `json:"compression,omitempty"`
	EditedBody     string              `json:"editedBody,omitempty"`
	BodyFile       string              `json:"bodyFile,omitempty"`
	BodySize       int64               `json:"bodySize,omitempty"`
	BodyHex        string              `json:"bodyHex,omitempty"`
	IsBinaryBody   bool                `json:"isBinaryBody,omitempty"`
	ParsedProtobuf []ProtobufField     `json:"parsedProtobuf,omitempty"`
}

type Store struct {
	dir      string
	mu       sync.Mutex
	index    []*ListEntry
	pending  []*Entry
	onSave   []func(entry *Entry)
	onUpdate []func(entry *Entry)
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) OnSave(fn func(entry *Entry)) {
	s.onSave = append(s.onSave, fn)
}

func (s *Store) OnUpdate(fn func(entry *Entry)) {
	s.onUpdate = append(s.onUpdate, fn)
}

type ListEntry struct {
	ID                  string    `json:"id"`
	Timestamp           time.Time `json:"timestamp"`
	UpdatedAt           time.Time `json:"updatedAt"`
	Method              string    `json:"method"`
	URL                 string    `json:"url"`
	Host                string    `json:"host"`
	Status              *int      `json:"status,omitempty"`
	ReplayedFrom        string    `json:"replayedFrom,omitempty"`
	AppliedAction       string    `json:"appliedAction,omitempty"`
	RuleName            string    `json:"ruleName,omitempty"`
	ClientProcess       string    `json:"clientProcess,omitempty"`
	ClientPID           uint32    `json:"clientPid,omitempty"`
	ClientDisplayName   string    `json:"clientDisplayName,omitempty"`
	Referer             string    `json:"referer,omitempty"`
	RequestContentType  string    `json:"requestContentType,omitempty"`
	ResponseContentType string    `json:"responseContentType,omitempty"`
	Origin              string    `json:"origin,omitempty"`
	AgentCallID         string    `json:"agentCallId,omitempty"`
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
			if err := s.buildIndex(); err != nil {
				return err
			}
			runtime.GC()
			debug.FreeOSMemory()
			return nil
		}
		return fmt.Errorf("read index: %w", err)
	}

	var index []*ListEntry
	if err := json.Unmarshal(data, &index); err != nil {
		logging.Log.Error("index.json is corrupt, rebuilding index")
		if err := s.buildIndex(); err != nil {
			return err
		}
		runtime.GC()
		debug.FreeOSMemory()
		return nil
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

	workers := max(min(runtime.NumCPU(), 2), 1)
	const batchSize = 500

	for i := 0; i < len(matches); i += batchSize {
		end := min(i+batchSize, len(matches))
		batch := matches[i:end]

		ch := make(chan string, len(batch))
		var mu sync.Mutex
		var wg sync.WaitGroup

		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for path := range ch {
					if le := s.parseEntryFile(path); le != nil {
						mu.Lock()
						s.index = append(s.index, le)
						mu.Unlock()
					}
				}
			}()
		}

		for _, p := range batch {
			ch <- p
		}
		close(ch)
		wg.Wait()

		debug.FreeOSMemory()
	}

	sort.Slice(s.index, func(i, j int) bool {
		return s.index[i].Timestamp.After(s.index[j].Timestamp)
	})

	return s.persistIndex()
}

func (s *Store) parseEntryFile(path string) *ListEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var raw struct {
		ID                string `json:"id"`
		Timestamp         string `json:"timestamp"`
		ReplayedFrom      string `json:"replayedFrom,omitempty"`
		AppliedAction     string `json:"appliedAction,omitempty"`
		RuleName          string `json:"ruleName,omitempty"`
		ClientProcess     string `json:"clientProcess,omitempty"`
		ClientPID         uint32 `json:"clientPid,omitempty"`
		ClientDisplayName string `json:"clientDisplayName,omitempty"`
		Origin            string `json:"origin,omitempty"`
		AgentCallID       string `json:"agentCallId,omitempty"`
		Request           struct {
			Method  string          `json:"method"`
			URL     string          `json:"url"`
			Host    string          `json:"host"`
			Headers json.RawMessage `json:"headers"`
		} `json:"request"`
		Response *struct {
			Status  int             `json:"status"`
			Headers json.RawMessage `json:"headers"`
		} `json:"response,omitempty"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	data = nil

	le := &ListEntry{
		ID:                raw.ID,
		Method:            raw.Request.Method,
		URL:               raw.Request.URL,
		Host:              raw.Request.Host,
		ReplayedFrom:      raw.ReplayedFrom,
		AppliedAction:     raw.AppliedAction,
		RuleName:          raw.RuleName,
		ClientProcess:     raw.ClientProcess,
		ClientPID:         raw.ClientPID,
		ClientDisplayName: raw.ClientDisplayName,
		Origin:            raw.Origin,
		AgentCallID:       raw.AgentCallID,
	}

	if len(raw.Request.Headers) > 0 {
		var headerFields struct {
			Referer     []string `json:"Referer"`
			ContentType []string `json:"Content-Type"`
		}
		json.Unmarshal(raw.Request.Headers, &headerFields)
		if len(headerFields.Referer) > 0 {
			le.Referer = headerFields.Referer[0]
		}
		if len(headerFields.ContentType) > 0 {
			le.RequestContentType = parseMediaType(headerFields.ContentType[0])
		}
	}

	if t, err := time.Parse(time.RFC3339Nano, raw.Timestamp); err == nil {
		le.Timestamp = t
		le.UpdatedAt = t
	}

	if raw.Response != nil {
		le.Status = &raw.Response.Status
		if len(raw.Response.Headers) > 0 {
			var respHeaderFields struct {
				ContentType []string `json:"Content-Type"`
			}
			json.Unmarshal(raw.Response.Headers, &respHeaderFields)
			if len(respHeaderFields.ContentType) > 0 {
				le.ResponseContentType = parseMediaType(respHeaderFields.ContentType[0])
			}
		}
	}

	return le
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
		Origin:            entry.Origin,
		AgentCallID:       entry.AgentCallID,
	}
	if refs, ok := entry.Request.Headers["Referer"]; ok && len(refs) > 0 {
		le.Referer = refs[0]
	}
	if cts, ok := entry.Request.Headers["Content-Type"]; ok && len(cts) > 0 {
		le.RequestContentType = parseMediaType(cts[0])
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

	for _, fn := range s.onSave {
		fn(entry)
	}

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

// GetByAgentCallID returns the ListEntry captured for an agent MCP call, matched
// by the correlation ID the interceptor stored when it stripped X-Gospy-Agent.
// The index is populated at Save time, so an entry is always findable here once
// the proxy has captured it.
func (s *Store) GetByAgentCallID(callID string) (*ListEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, le := range s.index {
		if le.AgentCallID == callID {
			return le, nil
		}
	}
	return nil, fmt.Errorf("no entry with agent call id %s", callID)
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
				if cts, ok := entry.Response.Headers["Content-Type"]; ok && len(cts) > 0 {
					le.ResponseContentType = parseMediaType(cts[0])
				}
			}
			le.UpdatedAt = time.Now()
			break
		}
	}
	err = s.persistIndex()
	s.mu.Unlock()

	for _, fn := range s.onUpdate {
		fn(entry)
	}

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
	if err := s.persistIndex(); err != nil {
		return err
	}
	binDir := filepath.Join(s.dir, "bin")
	os.RemoveAll(binDir)
	return nil
}

func (s *Store) SaveBinaryBody(entryID, suffix string, data []byte) (string, error) {
	binDir := filepath.Join(s.dir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", fmt.Errorf("create bin dir: %w", err)
	}
	filename := entryID + "-" + suffix + ".bin"
	path := filepath.Join(binDir, filename)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("write binary body: %w", err)
	}
	return filename, nil
}

func parseMediaType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i != -1 {
		ct = ct[:i]
	}
	return strings.TrimSpace(strings.ToLower(ct))
}
