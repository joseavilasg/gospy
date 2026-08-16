package webui

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"gospy/internal/history"
	"gospy/internal/proxy"
	"gospy/internal/rules"
	"gospy/internal/session"

	"google.golang.org/protobuf/encoding/protowire"
)

//go:embed index.html
var indexHTML string

//go:embed style.css
var styleCSS string

//go:embed app.js
var appJS string

//go:embed resize.js
var resizeJS string

//go:embed header.js
var headerJS string

//go:embed state.js
var stateJS string

//go:embed api.js
var apiJS string

//go:embed render.js
var renderJS string

//go:embed filters.js
var filtersJS string

//go:embed body-types.js
var bodyTypesJS string

//go:embed routes.js
var routesJS string

//go:embed json-viewer.js
var jsonViewerJS string

//go:embed json-viewer.css
var jsonViewerCSS string

//go:embed monaco-init.js
var monacoInitJS string

//go:embed monaco
var monacoFS embed.FS

type IgnoreChecker interface {
	IsIgnored(host string) bool
	Matches(host string) bool
	List() []string
	Add(host string) error
	Remove(host string) error
}

type FocusChecker interface {
	IsFocused(host string) bool
	Matches(host string) bool
	List() []string
	Add(host string) error
	Remove(host string) error
}

type Server struct {
	history     atomic.Pointer[history.Store]
	ignoreStore IgnoreChecker
	focusStore  FocusChecker
	rulesStore  *rules.Store
	engine      *rules.Engine
	filterStore *FilterStore
	addr        string
	proxyAddr   string
	resolver    ProcessResolver
	sigCache    SignatureChecker

	listMu          sync.Mutex
	lastVisible     map[string]bool
	totalMu         sync.Mutex
	nonIgnoredTotal int
	searchToken     uint64

	streamHub      *streamHub
	sessionStarter SessionStarter

	recordingHub *recordingHub

	replayMode   bool
	replayLogDir string

	// recordingStopped is set when the record max-duration is reached: new
	// traffic keeps flowing but nothing else is written to the session.
	recordingMu      sync.Mutex
	recordingStopped bool
	recordingMax     string

	replayMu      sync.Mutex
	replayRunID   string
	replayEvents  []session.ReplayEvent
	replayClients map[chan session.ReplayEvent]struct{}
}

// recordingEvent is the recording-state payload pushed to SSE clients.
type recordingEvent struct {
	Stopped bool   `json:"stopped"`
	Max     string `json:"max,omitempty"`
}

// recordingHub fans recording-state changes out to open SSE connections.
// Subscribers get the current state on subscribe (snapshot-on-connect) and
// every change afterwards.
type recordingHub struct {
	mu   sync.Mutex
	subs map[chan recordingEvent]struct{}
}

func newRecordingHub() *recordingHub {
	return &recordingHub{subs: make(map[chan recordingEvent]struct{})}
}

func (h *recordingHub) subscribe() chan recordingEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan recordingEvent, 4)
	h.subs[ch] = struct{}{}
	return ch
}

func (h *recordingHub) unsubscribe(ch chan recordingEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, ch)
}

func (h *recordingHub) publish(ev recordingEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// SessionStarter creates a recording session and rotates every consumer to it.
// Registered by main in record auto mode; nil otherwise.
type SessionStarter func(name string) (sessionDir, sessionName string, err error)

// maxBodyLen caps the body the detail endpoint serves in a single response.
// Streaming captures store the full body in the body file; the UI preview
// stops here and the body-bin endpoint serves the complete file.
const maxBodyLen = 2 * 1024 * 1024

type ProcessResolver interface {
	Resolve(remoteAddr string) *proxy.ProcessInfo
	GetAllProcesses() map[string]*proxy.ProcessInfo
}

type SignatureChecker interface {
	Get(filePath string) *proxy.SignatureResult
	VerifyAsync(filePath string)
	OnUpdate(fn func(*proxy.SignatureResult))
	Snapshot() []*proxy.SignatureResult
}

func NewServer(addr string, h *history.Store, ignore IgnoreChecker, focus FocusChecker, rulesStore *rules.Store, engine *rules.Engine, proxyAddr string, resolver ProcessResolver, sigCache SignatureChecker, filterStore *FilterStore) *Server {
	s := &Server{
		ignoreStore:   ignore,
		focusStore:    focus,
		rulesStore:    rulesStore,
		engine:        engine,
		filterStore:   filterStore,
		addr:          addr,
		proxyAddr:     proxyAddr,
		resolver:      resolver,
		sigCache:      sigCache,
		lastVisible:   make(map[string]bool),
		streamHub:     newStreamHub(h, maxBodyLen),
		recordingHub:  newRecordingHub(),
		replayClients: make(map[chan session.ReplayEvent]struct{}),
	}
	s.history.Store(h)
	s.attachStore(h)
	s.recomputeTotal()
	return s
}

// hist returns the current history store. The pointer is swapped atomically
// on session rotation so concurrent handlers serve the active session.
func (s *Server) hist() *history.Store {
	return s.history.Load()
}

// listSummary returns the index of the active session, or nil when no session
// is recording (record auto mode before the first session start).
func (s *Server) listSummary() []*history.ListEntry {
	if st := s.hist(); st != nil {
		return st.ListSummary()
	}
	return nil
}

// listSince returns the entries updated after since, or nil when no session is
// recording.
func (s *Server) listSince(since time.Time) []*history.ListEntry {
	if st := s.hist(); st != nil {
		return st.ListSince(since)
	}
	return nil
}

// getEntry returns the entry with the given id, writing a 404 when no session
// is recording or the entry does not exist.
func (s *Server) getEntry(w http.ResponseWriter, r *http.Request, id string) (*history.Entry, bool) {
	st := s.hist()
	if st == nil {
		http.Error(w, "no session recording active", http.StatusNotFound)
		return nil, false
	}
	entry, err := st.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	return entry, true
}

// attachStore subscribes the total counter to a store's save events. Called
// for the initial store and for every store on session rotation.
func (s *Server) attachStore(h *history.Store) {
	if h == nil {
		return
	}
	h.OnSave(func(e *history.Entry) {
		if !s.ignoreStore.Matches(e.Request.Host) {
			s.totalMu.Lock()
			s.nonIgnoredTotal++
			s.totalMu.Unlock()
		}
	})
}

// SetHistoryStore rotates the session: swaps the store, resets the visible-set
// snapshot and total, and bumps the filter version so the frontend refetches.
func (s *Server) SetHistoryStore(h *history.Store) {
	s.history.Store(h)
	s.attachStore(h)
	s.listMu.Lock()
	s.lastVisible = make(map[string]bool)
	s.listMu.Unlock()
	s.recomputeTotal()
	s.recordingMu.Lock()
	s.recordingStopped = false
	s.recordingMax = ""
	s.recordingMu.Unlock()
	s.filterStore.Touch()
	s.streamHub.SetHistoryStore(h)
}

// SetRecordingStopped marks that recording ended because max-duration was
// reached. Touching the filter store forces the frontend to refetch the list
// so the stopped indicator appears without a reload.
func (s *Server) SetRecordingStopped(max string) {
	s.recordingMu.Lock()
	s.recordingStopped = true
	s.recordingMax = max
	s.recordingMu.Unlock()
	s.recordingHub.publish(recordingEvent{Stopped: true, Max: max})
	s.filterStore.Touch()
}

// recordingState returns the current recording-stopped state.
func (s *Server) recordingState() (stopped bool, max string) {
	s.recordingMu.Lock()
	defer s.recordingMu.Unlock()
	return s.recordingStopped, s.recordingMax
}

// SetSessionStarter registers the callback that creates a recording session
// (record auto mode). Without it POST /api/session/start answers 400.
func (s *Server) SetSessionStarter(fn SessionStarter) {
	s.sessionStarter = fn
}

// SetReplayMode toggles the replay read-only mode: the UI keeps showing the
// recorded session but every mutating route answers 404 and session creation
// is refused.
func (s *Server) SetReplayMode(on bool) {
	s.replayMode = on
}

// SetReplayLogDir points the server at the directory holding replay runs
// (<dataDir>/replay/<session>). Used by the replay endpoints and the run
// selector.
func (s *Server) SetReplayLogDir(dir string) {
	s.replayLogDir = dir
}

// ReplayNotifier returns the callback to wire into the replay server. It keeps
// the active run's events in memory (for the list response, run endpoints and
// SSE snapshots) and fans every event out to connected stream clients.
func (s *Server) ReplayNotifier() func(ev session.ReplayEvent) {
	return func(ev session.ReplayEvent) {
		s.replayMu.Lock()
		if ev.RunID != s.replayRunID {
			s.replayRunID = ev.RunID
			s.replayEvents = nil
		}
		s.replayEvents = append(s.replayEvents, ev)
		clients := make([]chan session.ReplayEvent, 0, len(s.replayClients))
		for ch := range s.replayClients {
			clients = append(clients, ch)
		}
		s.replayMu.Unlock()
		for _, ch := range clients {
			select {
			case ch <- ev:
			default:
			}
		}
	}
}

// replayList is the replay state shipped inside every list response so the
// frontend chip and served badges survive reloads. Active refers to a run being
// written in this process; served is the set of hit entry IDs.
type replayList struct {
	Active    bool     `json:"active"`
	RunID     string   `json:"activeRunId,omitempty"`
	Consumed  int      `json:"consumed"`
	Total     int      `json:"total"`
	Exhausted bool     `json:"exhausted"`
	Served    []string `json:"served"`
}

// replayInfo returns the replay state for the list response, or nil when the
// server is not in replay mode.
func (s *Server) replayInfo() *replayList {
	if !s.replayMode {
		return nil
	}
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	if s.replayRunID == "" {
		return &replayList{}
	}
	info := &replayList{Active: true, RunID: s.replayRunID, Served: []string{}}
	for _, ev := range s.replayEvents {
		if ev.Result == "hit" && ev.EntryID != "" {
			info.Served = append(info.Served, ev.EntryID)
		}
	}
	if n := len(s.replayEvents); n > 0 {
		last := s.replayEvents[n-1]
		info.Consumed = last.Consumed
		info.Total = last.Total
		info.Exhausted = last.Exhausted
	}
	return info
}

// replayReadOnly rejects every request with 404 while the server is in replay
// mode, keeping the UI read-only.
func (s *Server) replayReadOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.replayMode {
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

// replayEventsFor returns the events of a run. The active run is served from
// the in-memory mirror; past runs are loaded from disk.
func (s *Server) replayEventsFor(runID string) ([]session.ReplayEvent, error) {
	s.replayMu.Lock()
	if runID == s.replayRunID {
		events := append([]session.ReplayEvent(nil), s.replayEvents...)
		s.replayMu.Unlock()
		return events, nil
	}
	s.replayMu.Unlock()
	dir, err := session.ReplayRunDir(s.replayLogDir, runID)
	if err != nil {
		return nil, err
	}
	return session.LoadReplayRun(dir)
}

func (s *Server) recomputeTotal() {
	entries := s.listSummary()
	total := 0
	for _, le := range entries {
		if !s.ignoreStore.Matches(le.Host) {
			total++
		}
	}
	s.totalMu.Lock()
	s.nonIgnoredTotal = total
	s.totalMu.Unlock()
}

func (s *Server) total() int {
	s.totalMu.Lock()
	defer s.totalMu.Unlock()
	return s.nonIgnoredTotal
}

func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/style.css", s.handleStatic(styleCSS, "text/css"))
	mux.HandleFunc("/app.js", s.handleStatic(appJS, "application/javascript"))
	mux.HandleFunc("/resize.js", s.handleStatic(resizeJS, "application/javascript"))
	mux.HandleFunc("/header.js", s.handleStatic(headerJS, "application/javascript"))
	mux.HandleFunc("/state.js", s.handleStatic(stateJS, "application/javascript"))
	mux.HandleFunc("/api.js", s.handleStatic(apiJS, "application/javascript"))
	mux.HandleFunc("/render.js", s.handleStatic(renderJS, "application/javascript"))
	mux.HandleFunc("/filters.js", s.handleStatic(filtersJS, "application/javascript"))
	mux.HandleFunc("/body-types.js", s.handleStatic(bodyTypesJS, "application/javascript"))
	mux.HandleFunc("/routes.js", s.handleStatic(routesJS, "application/javascript"))
	mux.HandleFunc("/json-viewer.js", s.handleStatic(jsonViewerJS, "application/javascript"))
	mux.HandleFunc("/json-viewer.css", s.handleStatic(jsonViewerCSS, "text/css"))
	mux.HandleFunc("/monaco-init.js", s.handleStatic(monacoInitJS, "application/javascript"))
	mux.HandleFunc("/monaco/", handleMonacoFile)
	mux.HandleFunc("/api/requests/search", s.handleSearch)
	mux.HandleFunc("/api/requests", s.handleListRequests)
	mux.HandleFunc("/api/requests/", s.handleGetRequest)
	mux.HandleFunc("/api/filters/options", s.handleFilterOptions)
	mux.HandleFunc("/api/filters/body", s.handleClearBodyFilter)
	mux.HandleFunc("/api/filters", s.handleSaveFilters)
	mux.HandleFunc("/api/agent/view", s.replayReadOnly(s.handleAgentView))
	mux.HandleFunc("/api/agent/enabled", s.replayReadOnly(s.handleAgentEnabled))
	mux.HandleFunc("/api/ignored", s.replayReadOnly(s.handleIgnored))
	mux.HandleFunc("/api/ignored/", s.replayReadOnly(s.handleIgnoredHost))
	mux.HandleFunc("/api/focused", s.replayReadOnly(s.handleFocused))
	mux.HandleFunc("/api/focused/", s.replayReadOnly(s.handleFocusedHost))
	mux.HandleFunc("/api/rules/check-match", s.handleCheckMatch)
	mux.HandleFunc("/api/rules", s.handleRules)
	mux.HandleFunc("/api/rules/", s.handleRule)
	mux.HandleFunc("/api/request-rule", s.replayReadOnly(s.handleRequestRule))
	mux.HandleFunc("/api/process/signature", s.handleProcessSignature)
	mux.HandleFunc("/api/process/events", s.handleProcessEvents)
	mux.HandleFunc("/api/recording/events", s.handleRecordingEvents)
	mux.HandleFunc("/api/streams/", s.handleStreamEvents)
	mux.HandleFunc("/api/session/start", s.handleSessionStart)
	mux.HandleFunc("/api/replay/runs", s.handleReplayRuns)
	mux.HandleFunc("/api/replay/events", s.handleReplayEventsList)
	mux.HandleFunc("/api/replay/events/", s.handleReplayEventsSub)

	LogWebUI(s.addr)

	return http.ListenAndServe(s.addr, mux)
}

func handleMonacoFile(w http.ResponseWriter, r *http.Request) {
	ext := r.URL.Path[strings.LastIndex(r.URL.Path, "."):]
	switch ext {
	case ".js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".map":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	http.FileServer(http.FS(monacoFS)).ServeHTTP(w, r)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexHTML)
}

func (s *Server) handleStatic(content, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		fmt.Fprint(w, content)
	}
}

// Pagination bounds shared by every full-list consumer. The maximum is hard:
// it is enforced server-side so no client (browser or agent MCP) can request an
// unbounded page of the visible set.
const (
	defaultPageSize = 1000
	maxPageSize     = 1000
)

// pageLimits normalizes and clamps an offset/limit pair. offset is floored at 0
// and limit is clamped to [defaultPageSize, maxPageSize]; an absent or invalid
// limit becomes the default page size.
func pageLimits(offset, limit int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	return offset, limit
}

func queryInt(r *http.Request, name string) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return 0
	}
	return v
}

type listResponse struct {
	Entries          []*history.ListEntry `json:"entries"`
	Upserts          []*history.ListEntry `json:"upserts"`
	Removed          []string             `json:"removed,omitempty"`
	Total            int                  `json:"total"`
	VisibleCount     int                  `json:"visibleCount"`
	Offset           int                  `json:"offset"`
	Version          int                  `json:"version"`
	Filters          *history.Filters     `json:"filters,omitempty"`
	FocusEnabled     bool                 `json:"focusEnabled"`
	AgentPreview     bool                 `json:"agentPreview"`
	AgentEnabled     bool                 `json:"agentEnabled"`
	AgentExposed     bool                 `json:"agentExposed"`
	Replay           *replayList          `json:"replay,omitempty"`
	RecordingStopped bool                 `json:"recordingStopped,omitempty"`
	RecordingMax     string               `json:"recordingMax,omitempty"`
}

// agentExposed reports whether the agent MCP is active AND its profile has no
// active filters - i.e. it would see the whole unfiltered traffic stream.
func (s *Server) agentExposed() bool {
	if !s.filterStore.AgentGate() {
		return false
	}
	f, focusEnabled, _ := s.filterStore.SnapshotAgent()
	if focusEnabled && len(s.focusStore.List()) > 0 {
		return false
	}
	return f.Empty()
}

func (s *Server) matchOpts(focusEnabled bool) history.MatchOpts {
	return history.MatchOpts{
		Ignored:      s.ignoreStore,
		Focused:      s.focusStore,
		FocusEnabled: focusEnabled,
	}
}

func (s *Server) handleListRequests(w http.ResponseWriter, r *http.Request) {
	sinceStr := r.URL.Query().Get("since")
	verStr := r.URL.Query().Get("version")

	if sinceStr != "" && verStr != "" {
		if t, err := time.Parse(time.RFC3339Nano, sinceStr); err == nil {
			if v, err := strconv.Atoi(verStr); err == nil && v == s.filterStore.Version() {
				s.writeJSON(w, s.diffList(t))
				return
			}
		}
	}

	offset, limit := pageLimits(queryInt(r, "offset"), queryInt(r, "limit"))
	s.writeJSON(w, s.fullList(offset, limit))
}

func (s *Server) fullList(offset, limit int) listResponse {
	filters, focusEnabled, version := s.filterStore.Snapshot()
	opts := s.matchOpts(focusEnabled)

	entries := s.listSummary()

	s.listMu.Lock()
	defer s.listMu.Unlock()
	lastVisible := make(map[string]bool)
	page, total, visibleCount := history.PageVisibleSet(entries,
		func(host string) bool { return s.ignoreStore.Matches(host) },
		func(le *history.ListEntry) bool { return filters.Matches(le, opts) },
		lastVisible, offset, limit)
	s.lastVisible = lastVisible

	s.totalMu.Lock()
	s.nonIgnoredTotal = total
	s.totalMu.Unlock()

	stopped, rmax := s.recordingState()
	return listResponse{
		Entries:          page,
		Total:            total,
		VisibleCount:     visibleCount,
		Offset:           offset,
		Version:          version,
		Filters:          &filters,
		FocusEnabled:     focusEnabled,
		AgentPreview:     s.filterStore.AgentPreview(),
		AgentEnabled:     s.filterStore.AgentGate(),
		AgentExposed:     s.agentExposed(),
		Replay:           s.replayInfo(),
		RecordingStopped: stopped,
		RecordingMax:     rmax,
	}
}

func (s *Server) diffList(since time.Time) listResponse {
	filters, focusEnabled, version := s.filterStore.Snapshot()
	opts := s.matchOpts(focusEnabled)

	s.listMu.Lock()
	defer s.listMu.Unlock()

	upserts := make([]*history.ListEntry, 0)
	removed := make([]string, 0)

	for _, le := range s.listSince(since) {
		isVisible := filters.Matches(le, opts)
		wasVisible := s.lastVisible[le.ID]
		if isVisible {
			upserts = append(upserts, le)
			s.lastVisible[le.ID] = true
		} else if wasVisible {
			removed = append(removed, le.ID)
			delete(s.lastVisible, le.ID)
		}
	}

	stopped, rmax := s.recordingState()
	return listResponse{
		Upserts:          upserts,
		Removed:          removed,
		Total:            s.total(),
		VisibleCount:     len(s.lastVisible),
		Version:          version,
		FocusEnabled:     focusEnabled,
		AgentPreview:     s.filterStore.AgentPreview(),
		AgentEnabled:     s.filterStore.AgentGate(),
		AgentExposed:     s.agentExposed(),
		Replay:           s.replayInfo(),
		RecordingStopped: stopped,
		RecordingMax:     rmax,
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(v)
}

type saveFiltersRequest struct {
	Filters      history.Filters `json:"filters"`
	FocusEnabled bool            `json:"focusEnabled"`
}

type agentViewRequest struct {
	Preview bool `json:"preview"`
}

type sessionStartRequest struct {
	Name string `json:"name"`
}

// handleSessionStart creates a new recording session (record auto mode). The
// body is optional: {"name":"..."} for a named session, empty for an auto name
// by timestamp.
func (s *Server) handleSessionStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.replayMode {
		http.Error(w, "replay mode: no recording", http.StatusConflict)
		return
	}
	starter := s.sessionStarter
	if starter == nil {
		http.Error(w, "no session recording active", http.StatusBadRequest)
		return
	}
	var req sessionStartRequest
	if r.Body != nil {
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil && err != io.EOF {
			http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	dir, name, err := starter(req.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]string{"session": dir, "name": name})
}

func (s *Server) handleAgentView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req agentViewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	s.filterStore.ClearBodyAll()
	s.filterStore.SetAgentPreview(req.Preview)
	s.writeJSON(w, s.fullList(0, defaultPageSize))
}

type agentEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) handleAgentEnabled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req agentEnabledRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	s.filterStore.SetAgentGate(req.Enabled)
	s.writeJSON(w, map[string]bool{
		"enabled": s.filterStore.AgentGate(),
		"exposed": s.agentExposed(),
	})
}

func (s *Server) handleSaveFilters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req saveFiltersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	s.filterStore.Set(req.Filters, req.FocusEnabled)
	s.writeJSON(w, s.fullList(0, defaultPageSize))
}

func (s *Server) handleFilterOptions(w http.ResponseWriter, r *http.Request) {
	typ := r.URL.Query().Get("type")
	entries := s.listSummary()
	s.writeJSON(w, map[string]any{
		"values": history.Options(entries, typ, s.ignoreStore),
	})
}

func (s *Server) handleClearBodyFilter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.filterStore.ClearBodyAll()
	w.WriteHeader(http.StatusNoContent)
}

func searchResponseBody(resp *history.ResponseRecord) string {
	if resp == nil {
		return ""
	}
	if resp.Body != "" {
		return resp.Body
	}
	if resp.RawBody == "" {
		return ""
	}
	ces := resp.Headers["Content-Encoding"]
	if len(ces) > 0 {
		result := history.DecompressBody([]byte(resp.RawBody), ces[0])
		if len(strings.TrimSpace(result.Decoded)) > 0 {
			return result.Decoded
		}
		if result.Compression != "" {
			return ""
		}
	}
	return resp.RawBody
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Q string `json:"q"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	q := strings.ToLower(strings.TrimSpace(req.Q))
	if len(q) < 3 {
		http.Error(w, "query too short", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")

	s.searchToken++
	token := s.searchToken
	profile := s.filterStore.ActiveProfile()
	s.filterStore.SetBodyIDsFor(profile, nil, token)

	entries := s.listSummary()
	enc := json.NewEncoder(w)
	var ids []string
	scanned := 0
	total := len(entries)

	commit := func() {
		if len(ids) == 0 {
			return
		}
		s.filterStore.SetBodyIDsFor(profile, ids, token)
	}

	for i := len(entries) - 1; i >= 0; i-- {
		id := entries[i].ID
		select {
		case <-r.Context().Done():
			s.filterStore.ClearBodyFor(profile, token)
			return
		default:
		}

		entry, err := s.hist().Get(id)
		if err != nil {
			continue
		}
		scanned++

		matched := false
		if !entry.Request.IsBinaryBody && entry.Request.Body != "" {
			if strings.Contains(strings.ToLower(entry.Request.Body), q) {
				matched = true
			}
		}

		if !matched && entry.Response != nil && !entry.Response.IsBinaryBody {
			body := searchResponseBody(entry.Response)
			if body != "" && strings.Contains(strings.ToLower(body), q) {
				matched = true
			}
		}

		if matched {
			ids = append(ids, id)
		}

		if scanned%5 == 0 || scanned == total {
			commit()
			enc.Encode(map[string]any{
				"scanned": scanned,
				"total":   total,
			})
			flusher.Flush()
		}
	}

	commit()
	enc.Encode(map[string]any{
		"done":       true,
		"matchCount": len(ids),
	})
	flusher.Flush()
}

const hexDumpMaxLines = 20

func generateHexDump(data []byte, maxLines int) string {
	if len(data) == 0 {
		return ""
	}
	maxBytes := maxLines * 16
	if len(data) < maxBytes {
		maxBytes = len(data)
	}
	var sb strings.Builder
	for i := 0; i < maxBytes; i += 16 {
		end := i + 16
		if end > maxBytes {
			end = maxBytes
		}
		chunk := data[i:end]
		fmt.Fprintf(&sb, "%08x: ", i)
		hex := make([]string, 0, 16)
		ascii := make([]byte, 0, 16)
		for j, b := range chunk {
			hex = append(hex, fmt.Sprintf("%02x", b))
			if j == 7 {
				hex = append(hex, "")
			}
			if b >= 32 && b <= 126 {
				ascii = append(ascii, b)
			} else {
				ascii = append(ascii, '.')
			}
		}
		fmt.Fprintf(&sb, "%-49s  %s\n", strings.Join(hex, " "), string(ascii))
	}
	if len(data) > maxBytes {
		fmt.Fprintf(&sb, "... (%d more bytes)\n", len(data)-maxBytes)
	}
	return sb.String()
}

func isProtobufContentType(ct string) bool {
	lct := strings.ToLower(ct)
	return strings.Contains(lct, "protobuf") || strings.Contains(lct, "x-protobuf")
}

const protobufMaxDepth = 12

func parseProtobufWire(data []byte) []history.ProtobufField {
	return parseProtobufWireAtDepth(data, 0)
}

func parseProtobufWireAtDepth(data []byte, depth int) []history.ProtobufField {
	if depth >= protobufMaxDepth {
		return nil
	}
	var fields []history.ProtobufField
	offset := 0
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			break
		}
		data = data[n:]

		fieldStart := offset
		offset += n

		f := history.ProtobufField{
			FieldNumber: int(num),
			ByteOffset:  fieldStart,
		}

		switch typ {
		case protowire.VarintType:
			v, n2 := protowire.ConsumeVarint(data)
			if n2 < 0 {
				return fields
			}
			f.WireType = "varint"
			f.ZigzagValue = int64((v >> 1) ^ -(v & 1))
			if v <= uint64(^uint32(0)) {
				f.Value = uint32(v)
			} else {
				f.Value = v
			}
			f.ByteEnd = offset + n2
			offset += n2
			data = data[n2:]

		case protowire.Fixed32Type:
			v, n2 := protowire.ConsumeFixed32(data)
			if n2 < 0 {
				return fields
			}
			f.WireType = "fixed32"
			f.Value = v
			f.ByteEnd = offset + n2
			offset += n2
			data = data[n2:]

		case protowire.Fixed64Type:
			v, n2 := protowire.ConsumeFixed64(data)
			if n2 < 0 {
				return fields
			}
			f.WireType = "fixed64"
			f.Value = v
			f.ByteEnd = offset + n2
			offset += n2
			data = data[n2:]

		case protowire.BytesType:
			v, n2 := protowire.ConsumeBytes(data)
			if n2 < 0 {
				return fields
			}
			f.ByteSize = len(v)
			f.ByteEnd = offset + n2
			offset += n2
			data = data[n2:]

			subFields := parseProtobufWireAtDepth(v, depth+1)
			if len(subFields) > 0 {
				f.WireType = "message"
				f.SubFields = subFields
			} else if s, ok := isPrintableUTF8(v); ok {
				f.WireType = "string"
				f.Value = s
			} else {
				f.WireType = "bytes"
				f.Value = fmt.Sprintf("%x", v)
			}

		default:
			n2 := protowire.ConsumeFieldValue(num, typ, data)
			if n2 < 0 {
				return fields
			}
			f.WireType = fmt.Sprintf("type(%d)", int(typ))
			f.ByteEnd = offset + n2
			offset += n2
			data = data[n2:]
		}

		fields = append(fields, f)
	}
	return fields
}

func isPrintableUTF8(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	if !utf8.Valid(b) {
		return "", false
	}
	s := string(b)
	for _, r := range s {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			return "", false
		}
	}
	return s, true
}

func extractBoundary(contentType string) string {
	_, after, ok := strings.Cut(contentType, "boundary=")
	if !ok {
		return ""
	}
	b := after
	b = strings.Trim(b, "\"' ")
	return b
}

const multipartHexPreviewLines = 20

func parseMultipartBody(data []byte, boundary string) []history.MultipartPart {
	if boundary == "" || len(data) == 0 {
		return nil
	}
	reader := multipart.NewReader(bytes.NewReader(data), boundary)
	var parts []history.MultipartPart
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		name := part.FormName()
		if name == "" {
			name = part.FileName()
		}
		mp := history.MultipartPart{
			Name:        name,
			Filename:    part.FileName(),
			ContentType: part.Header.Get("Content-Type"),
		}
		content, err := io.ReadAll(part)
		if err != nil {
			parts = append(parts, mp)
			continue
		}
		mp.Size = len(content)
		if mp.Filename != "" || (!isLikelyTextContentType(mp.ContentType) && containsNullBytes(content)) {
			mp.IsBinary = true
			mp.HexPreview = generateHexDump(content, multipartHexPreviewLines)
		} else {
			mp.Value = string(content)
		}
		parts = append(parts, mp)
	}
	return parts
}

func isLikelyTextContentType(ct string) bool {
	ct = strings.ToLower(ct)
	for _, prefix := range []string{
		"text/", "application/json", "application/xml",
		"application/x-www-form-urlencoded", "application/javascript",
		"application/css", "application/x-yaml", "application/yaml",
	} {
		if strings.HasPrefix(ct, prefix) {
			return true
		}
	}
	return false
}

func containsNullBytes(data []byte) bool {
	limit := 8192
	if len(data) < limit {
		limit = len(data)
	}
	for _, b := range data[:limit] {
		if b == 0 {
			return true
		}
	}
	return false
}

// entryDetailResponse pairs an entry with its resolved client signature (when
// already known) so the UI can render the origin verdict without an extra fetch.
type entryDetailResponse struct {
	*history.Entry
	ClientSignature *proxy.SignatureResult `json:"clientSignature,omitempty"`
}

func (s *Server) handleGetRequest(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/requests/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]

	if id == "" {
		http.NotFound(w, r)
		return
	}

	if len(parts) > 1 {
		sub := parts[1]
		if s.replayMode {
			switch {
			case sub == "body-bin" && r.Method == http.MethodGet:
				s.handleGetBodyBin(w, r, id)
				return
			case sub == "curl" && r.Method == http.MethodGet:
				s.handleCopyCurl(w, r, id)
				return
			default:
				http.NotFound(w, r)
				return
			}
		}
		switch {
		case sub == "body" && r.Method == http.MethodPut:
			s.handleSaveBody(w, r, id)
		case sub == "body" && r.Method == http.MethodDelete:
			s.handleRevertBody(w, r, id)
		case sub == "headers" && r.Method == http.MethodPut:
			s.handleSaveHeaders(w, r, id)
		case sub == "headers" && r.Method == http.MethodDelete:
			s.handleRevertHeaders(w, r, id)
		case sub == "replay" && r.Method == http.MethodPost:
			s.handleReplay(w, r, id)
		case sub == "body-bin" && r.Method == http.MethodGet:
			s.handleGetBodyBin(w, r, id)
		case sub == "curl" && r.Method == http.MethodGet:
			s.handleCopyCurl(w, r, id)
		case sub == "body-multipart" && r.Method == http.MethodPut:
			s.handleSaveMultipart(w, r, id)
		default:
			http.NotFound(w, r)
		}
		return
	}

	entry, ok := s.getEntry(w, r, id)
	if !ok {
		return
	}

	if entry.Request.RawBody != "" && entry.Request.Body == "" {
		ce := entry.Request.Headers["Content-Encoding"]
		enc := ""
		if len(ce) > 0 {
			enc = ce[0]
		}
		entry.Request.Body = history.DecompressBody([]byte(entry.Request.RawBody), enc).Decoded
	}
	if entry.Response != nil && entry.Response.RawBody != "" && entry.Response.Body == "" {
		ce := entry.Response.Headers["Content-Encoding"]
		enc := ""
		if len(ce) > 0 {
			enc = ce[0]
		}
		entry.Response.Body = history.DecompressBody([]byte(entry.Response.RawBody), enc).Decoded
	}
	if entry.ServerResponse != nil && entry.ServerResponse.RawBody != "" && entry.ServerResponse.Body == "" {
		ce := entry.ServerResponse.Headers["Content-Encoding"]
		enc := ""
		if len(ce) > 0 {
			enc = ce[0]
		}
		entry.ServerResponse.Body = history.DecompressBody([]byte(entry.ServerResponse.RawBody), enc).Decoded
	}

	if len(entry.Request.Body) > maxBodyLen {
		entry.Request.Body = entry.Request.Body[:maxBodyLen] + "\n... [truncated - body too large]"
	}
	if entry.Response != nil && len(entry.Response.Body) > maxBodyLen {
		entry.Response.Body = entry.Response.Body[:maxBodyLen] + "\n... [truncated - body too large]"
	}
	if entry.ServerResponse != nil && len(entry.ServerResponse.Body) > maxBodyLen {
		entry.ServerResponse.Body = entry.ServerResponse.Body[:maxBodyLen] + "\n... [truncated - body too large]"
	}

	binDir := filepath.Join(s.hist().Dir(), "bin")

	if entry.Request.BodyFile != "" {
		var reqCT string
		if cts, ok := entry.Request.Headers["Content-Type"]; ok && len(cts) > 0 {
			reqCT = cts[0]
		}
		if strings.Contains(strings.ToLower(reqCT), "multipart/form-data") {
			if boundary := extractBoundary(reqCT); boundary != "" {
				if data, err := os.ReadFile(filepath.Join(binDir, entry.Request.BodyFile)); err == nil {
					entry.Request.ParsedMultipart = parseMultipartBody(data, boundary)
				}
			}
		}
		if isProtobufContentType(reqCT) {
			if data, err := os.ReadFile(filepath.Join(binDir, entry.Request.BodyFile)); err == nil {
				entry.Request.ParsedProtobuf = parseProtobufWire(data)
			}
		}
	}

	if entry.Request.BodyFile != "" {
		needHex := entry.Request.IsBinaryBody
		if !needHex {
			for _, p := range entry.Request.ParsedMultipart {
				if p.IsBinary {
					needHex = true
					break
				}
			}
		}
		if needHex {
			if data, err := os.ReadFile(filepath.Join(binDir, entry.Request.BodyFile)); err == nil {
				entry.Request.BodyHex = generateHexDump(data, hexDumpMaxLines)
			}
		}
	}
	if entry.Response != nil && entry.Response.BodyFile != "" {
		var respCT string
		if cts, ok := entry.Response.Headers["Content-Type"]; ok && len(cts) > 0 {
			respCT = cts[0]
		}
		if isProtobufContentType(respCT) {
			if data, err := os.ReadFile(filepath.Join(binDir, entry.Response.BodyFile)); err == nil {
				entry.Response.ParsedProtobuf = parseProtobufWire(data)
			}
		}
		if entry.Response.IsBinaryBody {
			if data, err := os.ReadFile(filepath.Join(binDir, entry.Response.BodyFile)); err == nil {
				entry.Response.BodyHex = generateHexDump(data, hexDumpMaxLines)
			}
		}
		if !entry.Response.IsBinaryBody && entry.Response.Body == "" {
			if preview, ok := readBodyPreview(filepath.Join(binDir, entry.Response.BodyFile), maxBodyLen); ok {
				entry.Response.Body = preview
			}
		}
	}
	if entry.ServerResponse != nil && entry.ServerResponse.BodyFile != "" && entry.ServerResponse.IsBinaryBody {
		if data, err := os.ReadFile(filepath.Join(binDir, entry.ServerResponse.BodyFile)); err == nil {
			entry.ServerResponse.BodyHex = generateHexDump(data, hexDumpMaxLines)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(entryDetailResponse{Entry: entry, ClientSignature: s.resolveClientSignature(entry.ClientPath)})
}

func (s *Server) handleGetBodyBin(w http.ResponseWriter, r *http.Request, id string) {
	target := r.URL.Query().Get("target")
	if target != "request" && target != "response" {
		http.Error(w, "invalid target", http.StatusBadRequest)
		return
	}

	entry, ok := s.getEntry(w, r, id)
	if !ok {
		return
	}

	var bodyFile string
	if target == "request" {
		bodyFile = entry.Request.BodyFile
	} else if entry.Response != nil {
		bodyFile = entry.Response.BodyFile
	}

	if bodyFile == "" {
		http.NotFound(w, r)
		return
	}

	binPath := filepath.Join(s.hist().Dir(), "bin", bodyFile)
	f, err := os.Open(binPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.bin"`, id, target))
	w.Header().Set("Access-Control-Allow-Origin", "*")
	io.Copy(w, f)
}

// readBodyPreview reads up to limit bytes of a body file and appends the
// truncation marker when the file is larger. Used for text bodies that live
// only as files (streaming captures), so huge files stay cheap to serve.
func readBodyPreview(path string, limit int) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", false
	}
	if len(data) > limit {
		return string(data[:limit]) + "\n... [truncated - body too large]", true
	}
	return string(data), true
}

func (s *Server) handleSaveMultipart(w http.ResponseWriter, r *http.Request, id string) {
	entry, ok := s.getEntry(w, r, id)
	if !ok {
		return
	}

	var req struct {
		Parts []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"parts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var reqCT string
	if cts, ok := entry.Request.Headers["Content-Type"]; ok && len(cts) > 0 {
		reqCT = cts[0]
	}
	boundary := extractBoundary(reqCT)
	if boundary == "" {
		http.Error(w, "not multipart/form-data", http.StatusBadRequest)
		return
	}

	binDir := filepath.Join(s.hist().Dir(), "bin")
	origData, err := os.ReadFile(filepath.Join(binDir, entry.Request.BodyFile))
	if err != nil {
		http.Error(w, "original body not found", http.StatusInternalServerError)
		return
	}

	modifiedValues := make(map[string]string)
	for _, p := range req.Parts {
		modifiedValues[p.Name] = p.Value
	}

	reader := multipart.NewReader(bytes.NewReader(origData), boundary)
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.SetBoundary(boundary)

	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		name := part.FormName()
		if name == "" {
			name = part.FileName()
		}
		header := make(map[string][]string)
		for k, v := range part.Header {
			header[k] = v
		}

		if newVal, ok := modifiedValues[name]; ok && part.FileName() == "" {
			if err := writer.WriteField(name, newVal); err != nil {
				http.Error(w, "write field failed", http.StatusInternalServerError)
				return
			}
		} else {
			p, err := writer.CreatePart(header)
			if err != nil {
				http.Error(w, "create part failed", http.StatusInternalServerError)
				return
			}
			if _, err := io.Copy(p, part); err != nil {
				http.Error(w, "copy part failed", http.StatusInternalServerError)
				return
			}
		}
	}
	writer.Close()

	entry.Request.EditedBody = buf.String()
	if err := s.hist().Update(entry); err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(entry)
}

func (s *Server) handleCopyCurl(w http.ResponseWriter, r *http.Request, id string) {
	entry, ok := s.getEntry(w, r, id)
	if !ok {
		return
	}

	proxyHost := r.URL.Query().Get("proxyHost")

	req := entry.Request
	method := req.Method
	reqURL := req.URL
	if reqURL == "" {
		reqURL = "http://" + req.Host
	}

	skipHeaders := map[string]bool{
		"host":             true,
		"proxy-connection": true,
		"content-length":   true,
	}

	var lines []string

	innerParts := []string{"curl", "--ssl-no-revoke"}

	if s.proxyAddr != "" {
		proxyURL := "http://127.0.0.1" + s.proxyAddr
		innerParts = append(innerParts, "-x", fmt.Sprintf("'%s'", proxyURL))
	}

	innerParts = append(innerParts, fmt.Sprintf("'%s'", reqURL))

	innerParts = append(innerParts, "-X", method)

	// Sort headers alphabetically
	type headerPair struct {
		Key   string
		Value string
	}
	var headers []headerPair
	for k, vals := range req.Headers {
		if skipHeaders[strings.ToLower(k)] {
			continue
		}
		val := strings.Join(vals, ", ")
		headers = append(headers, headerPair{k, val})
	}
	sort.Slice(headers, func(i, j int) bool {
		return strings.ToLower(headers[i].Key) < strings.ToLower(headers[j].Key)
	})

	for _, h := range headers {
		if h.Value == "" {
			innerParts = append(innerParts, "-H", fmt.Sprintf("'%s;'", h.Key))
		} else {
			innerParts = append(innerParts, "-H", fmt.Sprintf("'%s: %s'", h.Key, h.Value))
		}
	}

	// Body
	if req.Body != "" {
		escaped := strings.ReplaceAll(req.Body, "'", "'\\''")
		innerParts = append(innerParts, "--data-raw", fmt.Sprintf("'%s'", escaped))
	} else if req.BodyFile != "" {
		// Binary - pipe from GoSpy
		innerParts = append(innerParts, "--data-binary", "@-")
	}

	// Format with line continuations
	if len(innerParts) <= 3 {
		// Short command - single line: curl 'URL'
		lines = append(lines, strings.Join(innerParts, " "))
	} else {
		lines = append(lines, innerParts[0]+" "+innerParts[1]+" \\")
		for i := 2; i < len(innerParts); i += 2 {
			flag := innerParts[i]
			value := ""
			if i+1 < len(innerParts) {
				value = " " + innerParts[i+1]
			}
			if i+2 < len(innerParts) {
				lines = append(lines, fmt.Sprintf("  %s%s \\", flag, value))
			} else {
				lines = append(lines, fmt.Sprintf("  %s%s", flag, value))
			}
		}
	}

	// Wrap with pipe line for binary bodies
	if req.BodyFile != "" && proxyHost != "" {
		pipeURL := fmt.Sprintf("%s/api/requests/%s/body-bin?target=request", proxyHost, id)
		pipeLine := fmt.Sprintf("curl -s '%s' | \\", pipeURL)
		lines = append([]string{pipeLine}, lines...)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	fmt.Fprint(w, strings.Join(lines, "\n"))
}

func (s *Server) handleSaveBody(w http.ResponseWriter, r *http.Request, id string) {
	if s.hist() == nil {
		http.Error(w, "no session recording active", http.StatusNotFound)
		return
	}
	var body struct {
		Target string `json:"target"`
		Body   string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	if body.Target != "request" && body.Target != "response" {
		http.Error(w, `{"error":"target must be request or response"}`, http.StatusBadRequest)
		return
	}

	if err := s.hist().SaveEditedBody(id, body.Target, body.Body); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleRevertBody(w http.ResponseWriter, r *http.Request, id string) {
	if s.hist() == nil {
		http.Error(w, "no session recording active", http.StatusNotFound)
		return
	}
	target := r.URL.Query().Get("target")
	if target != "request" && target != "response" {
		http.Error(w, `{"error":"target must be request or response"}`, http.StatusBadRequest)
		return
	}

	if err := s.hist().RevertBody(id, target); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleSaveHeaders(w http.ResponseWriter, r *http.Request, id string) {
	if s.hist() == nil {
		http.Error(w, "no session recording active", http.StatusNotFound)
		return
	}
	var payload struct {
		Headers map[string][]string `json:"headers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}

	if err := s.hist().SaveEditedHeaders(id, payload.Headers); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleRevertHeaders(w http.ResponseWriter, r *http.Request, id string) {
	if s.hist() == nil {
		http.Error(w, "no session recording active", http.StatusNotFound)
		return
	}
	if err := s.hist().RevertHeaders(id); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request, id string) {
	var bodyOverride struct {
		Body string `json:"body"`
	}
	json.NewDecoder(r.Body).Decode(&bodyOverride)

	original, ok := s.getEntry(w, r, id)
	if !ok {
		return
	}

	reqURL := original.Request.URL
	if reqURL == "" {
		host := original.Request.Host
		if !strings.HasPrefix(host, "http") {
			host = "http://" + host
		}
		reqURL = host
	}

	var reqBody io.Reader
	if bodyOverride.Body != "" {
		reqBody = strings.NewReader(bodyOverride.Body)
	} else if original.Request.Body != "" {
		reqBody = strings.NewReader(original.Request.Body)
	}

	httpReq, err := http.NewRequestWithContext(r.Context(), original.Request.Method, reqURL, reqBody)
	if err != nil {
		http.Error(w, `{"error":"failed to build request: `+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	skipHeaders := map[string]bool{
		"Host": true, "Proxy-Connection": true, "Accept-Encoding": true,
		"Connection": true, "Proxy-Authorization": true,
	}
	headers := original.Request.Headers
	if original.Request.EditedHeaders != nil {
		headers = original.Request.EditedHeaders
	}
	for k, v := range headers {
		if !skipHeaders[k] {
			httpReq.Header[k] = v
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)

	newEntry := &history.Entry{
		Request: history.RequestRecord{
			Method:  original.Request.Method,
			URL:     original.Request.URL,
			Host:    original.Request.Host,
			Headers: headers,
			Body:    bodyOverride.Body,
		},
		ReplayedFrom: original.ID,
	}

	if err == nil {
		defer resp.Body.Close()
		respBodyBytes, _ := io.ReadAll(resp.Body)
		newEntry.Response = &history.ResponseRecord{
			Status:  resp.StatusCode,
			Headers: resp.Header,
			Body:    string(respBodyBytes),
		}
	}

	if err := s.hist().Save(newEntry); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	proxy.LogRequest(newEntry.ID, original.Request.Method, reqURL)
	if err == nil {
		if vals, ok := newEntry.Response.Headers["Content-Type"]; ok && len(vals) > 0 {
			proxy.LogResponse(newEntry.ID, original.Request.Method, reqURL, newEntry.Response.Status, vals[0])
		} else {
			proxy.LogResponse(newEntry.ID, original.Request.Method, reqURL, newEntry.Response.Status, "")
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": newEntry.ID})
}

func (s *Server) handleIgnored(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	switch r.Method {
	case http.MethodGet:
		json.NewEncoder(w).Encode(s.ignoreStore.List())
	case http.MethodPost:
		var body struct {
			Host string `json:"host"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Host == "" {
			http.Error(w, `{"error":"invalid host"}`, http.StatusBadRequest)
			return
		}
		if err := s.ignoreStore.Add(body.Host); err != nil {
			http.Error(w, `{"error":"failed to add"}`, http.StatusInternalServerError)
			return
		}
		s.recomputeTotal()
		s.filterStore.Touch()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(s.ignoreStore.List())
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleIgnoredHost(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimPrefix(r.URL.Path, "/api/ignored/")
	if host == "" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method == http.MethodDelete {
		if err := s.ignoreStore.Remove(host); err != nil {
			http.Error(w, `{"error":"failed to remove"}`, http.StatusInternalServerError)
			return
		}
		s.recomputeTotal()
		s.filterStore.Touch()
		json.NewEncoder(w).Encode(s.ignoreStore.List())
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

func (s *Server) handleFocused(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	switch r.Method {
	case http.MethodGet:
		json.NewEncoder(w).Encode(s.focusStore.List())
	case http.MethodPost:
		var body struct {
			Host string `json:"host"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Host == "" {
			http.Error(w, `{"error":"invalid host"}`, http.StatusBadRequest)
			return
		}
		if err := s.focusStore.Add(body.Host); err != nil {
			http.Error(w, `{"error":"failed to add"}`, http.StatusInternalServerError)
			return
		}
		s.filterStore.Touch()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(s.focusStore.List())
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleFocusedHost(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimPrefix(r.URL.Path, "/api/focused/")
	if host == "" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method == http.MethodDelete {
		if err := s.focusStore.Remove(host); err != nil {
			http.Error(w, `{"error":"failed to remove"}`, http.StatusInternalServerError)
			return
		}
		s.filterStore.Touch()
		json.NewEncoder(w).Encode(s.focusStore.List())
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

func LogWebUI(addr string) {
	fmt.Printf("\033[36m%s\033[0m %s\n", "WEBUI", "http://"+addr)
}

func (s *Server) handleCheckMatch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Method     string `json:"method"`
		Host       string `json:"host"`
		URLPattern string `json:"url_pattern"`
		ExcludeID  string `json:"exclude_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}

	matches := s.engine.FindMatchingRules(req.Method, req.Host, req.URLPattern, req.ExcludeID)
	if matches == nil {
		matches = []*rules.Rule{}
	}
	json.NewEncoder(w).Encode(matches)
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	switch r.Method {
	case http.MethodGet:
		rulesList := s.rulesStore.GetRules()
		json.NewEncoder(w).Encode(rulesList)
	case http.MethodPost:
		var rule rules.Rule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			proxy.LogError(fmt.Sprintf("decode rule body: %v", err))
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		if rule.Action == rules.ActionModify {
			if rule.ModifiedReq == nil || rule.ModifiedReq.Host == "" {
				http.Error(w, `{"error":"host is required for modify action"}`, http.StatusBadRequest)
				return
			}
		}
		rule.ID = generateID()
		rule.Enabled = true
		rule.CreatedAt = time.Now()
		if err := s.rulesStore.AddRule(&rule); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		deactivated := s.rulesStore.DeactivateConflicts(rule.Match.Method, rule.Match.Host, rule.Match.URLPattern, rule.ID)
		s.engine.Load(s.rulesStore.GetRules())
		if deactivated == nil {
			deactivated = []string{}
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"rule": rule, "deactivated": deactivated})
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRule(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/rules/")
	if id == "" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	switch r.Method {
	case http.MethodPut:
		var rule rules.Rule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			proxy.LogError(fmt.Sprintf("decode rule body: %v", err))
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		rule.ID = id
		if rule.Action == rules.ActionModify {
			if rule.ModifiedReq == nil || rule.ModifiedReq.Host == "" {
				http.Error(w, `{"error":"host is required for modify action"}`, http.StatusBadRequest)
				return
			}
		}
		if err := s.rulesStore.UpdateRule(&rule); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		s.engine.Load(s.rulesStore.GetRules())
		json.NewEncoder(w).Encode(&rule)
	case http.MethodDelete:
		if err := s.rulesStore.RemoveRule(id); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		s.engine.Load(s.rulesStore.GetRules())
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	case http.MethodPatch:
		rulesList := s.rulesStore.GetRules()
		for _, rule := range rulesList {
			if rule.ID == id {
				rule.Enabled = !rule.Enabled
				if err := s.rulesStore.UpdateRule(rule); err != nil {
					http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
					return
				}
				var deactivated []string
				if rule.Enabled {
					deactivated = s.rulesStore.DeactivateConflicts(rule.Match.Method, rule.Match.Host, rule.Match.URLPattern, rule.ID)
				}
				s.engine.Load(s.rulesStore.GetRules())
				if deactivated == nil {
					deactivated = []string{}
				}
				json.NewEncoder(w).Encode(map[string]any{"rule": rule, "deactivated": deactivated})
				return
			}
		}
		http.Error(w, `{"error":"rule not found"}`, http.StatusNotFound)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRequestRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, `{"error":"id parameter required"}`, http.StatusBadRequest)
		return
	}

	entry, err := s.hist().Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(entry)
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// streamEvent is pushed over SSE to a detail panel subscribed to a live
// response. snapshot carries the current body, update a delta appended to it,
// and done finalizes the view (the frontend then refetches the entry for the
// authoritative final state).
type streamEvent struct {
	Type      string `json:"type"`
	BodySize  int64  `json:"bodySize,omitempty"`
	Stream    bool   `json:"stream,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Preview   string `json:"preview,omitempty"`
}

type streamSub struct {
	entryID  string
	bodyFile string
	lastSize int64
	ch       chan streamEvent
}

type streamOp struct {
	kind    string // subscribe | unsubscribe | notify | setstore
	entryID string
	size    int64
	done    bool
	sub     *streamSub
	store   *history.Store
}

// streamHub fans out body-growth notifications from the interceptor to the SSE
// subscribers of each entry. A single reactor goroutine serializes
// subscribe/unsubscribe/notify so delta computation never gaps or overlaps:
// lastSize only advances on a successful push, and a dropped update is
// re-sent as part of the next delta (ranges are cumulative).
type streamHub struct {
	history    *history.Store
	maxPreview int64
	opCh       chan streamOp
	subs       map[string]map[*streamSub]struct{}
}

func newStreamHub(h *history.Store, maxPreview int64) *streamHub {
	hub := &streamHub{
		history:    h,
		maxPreview: maxPreview,
		opCh:       make(chan streamOp, 256),
		subs:       make(map[string]map[*streamSub]struct{}),
	}
	go hub.run()
	return hub
}

func (h *streamHub) run() {
	for op := range h.opCh {
		switch op.kind {
		case "subscribe":
			h.subscribe(op)
		case "unsubscribe":
			h.unsubscribe(op)
		case "notify":
			h.notify(op)
		case "setstore":
			h.setStore(op)
		}
	}
}

// SetHistoryStore rotates the store the hub reads bodies through. Serialized
// through the op channel: the swap and the subscriber teardown both happen on
// the reactor goroutine, so no handler ever reads a half-rotated hub.
func (h *streamHub) SetHistoryStore(store *history.Store) {
	h.opCh <- streamOp{kind: "setstore", store: store}
}

func (h *streamHub) setStore(op streamOp) {
	h.history = op.store
	for _, subs := range h.subs {
		for sub := range subs {
			select {
			case sub.ch <- streamEvent{Type: "done", Stream: false}:
			default:
			}
		}
	}
	h.subs = make(map[string]map[*streamSub]struct{})
}

func (h *streamHub) subscribe(op streamOp) {
	entry, err := h.history.Get(op.entryID)
	if err != nil || entry.Response == nil || entry.Response.BodyFile == "" {
		op.sub.ch <- streamEvent{Type: "done", Stream: false}
		return
	}
	op.sub.bodyFile = entry.Response.BodyFile
	// The finalize notifier fires after the Stream=false update, so a stream
	// that ends between this check and registration still delivers its done
	// event through the queued notify op (FIFO ordering).
	if !entry.Response.Stream {
		op.sub.ch <- streamEvent{Type: "done", BodySize: entry.Response.BodySize, Stream: false}
		return
	}
	subs := h.subs[op.entryID]
	if subs == nil {
		subs = make(map[*streamSub]struct{})
		h.subs[op.entryID] = subs
	}
	subs[op.sub] = struct{}{}
	size, truncated, preview := h.previewFile(op.sub.bodyFile)
	op.sub.lastSize = size
	op.sub.ch <- streamEvent{Type: "snapshot", BodySize: size, Stream: true, Truncated: truncated, Preview: preview}
}

func (h *streamHub) unsubscribe(op streamOp) {
	subs := h.subs[op.entryID]
	if subs == nil {
		return
	}
	delete(subs, op.sub)
	if len(subs) == 0 {
		delete(h.subs, op.entryID)
	}
}

func (h *streamHub) notify(op streamOp) {
	subs := h.subs[op.entryID]
	if len(subs) == 0 {
		return
	}
	if op.done {
		for sub := range subs {
			if sub.lastSize < op.size && sub.lastSize < h.maxPreview {
				end := op.size
				if end > h.maxPreview {
					end = h.maxPreview
				}
				delta := h.readRange(sub.bodyFile, sub.lastSize, end)
				select {
				case sub.ch <- streamEvent{Type: "update", BodySize: op.size, Stream: true, Truncated: op.size > h.maxPreview, Preview: delta}:
				default:
				}
			}
			// The final done event is best-effort: a stalled subscriber must
			// not stall the whole hub, and the frontend has an onerror
			// fallback that refetches the detail when the stream goes quiet.
			select {
			case sub.ch <- streamEvent{Type: "done", BodySize: op.size, Stream: false}:
			default:
			}
		}
		delete(h.subs, op.entryID)
		return
	}
	for sub := range subs {
		if op.size <= sub.lastSize {
			continue
		}
		if sub.lastSize >= h.maxPreview {
			sub.lastSize = op.size
			continue
		}
		ev := streamEvent{Type: "update", BodySize: op.size, Stream: true}
		if op.size <= h.maxPreview {
			ev.Preview = h.readRange(sub.bodyFile, sub.lastSize, op.size)
		} else {
			ev.Truncated = true
			ev.Preview = h.readRange(sub.bodyFile, sub.lastSize, h.maxPreview)
		}
		select {
		case sub.ch <- ev:
			sub.lastSize = op.size
		default:
		}
	}
}

func (h *streamHub) previewFile(bodyFile string) (size int64, truncated bool, preview string) {
	f, err := os.Open(filepath.Join(h.history.Dir(), "bin", bodyFile))
	if err != nil {
		return 0, false, ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, false, ""
	}
	size = fi.Size()
	// Read only up to the preview cap: the full capture can grow far beyond
	// the view limit and must never be loaded into memory whole.
	data, err := io.ReadAll(io.LimitReader(f, h.maxPreview))
	if err != nil {
		return 0, false, ""
	}
	return size, size > h.maxPreview, string(data)
}

func (h *streamHub) readRange(bodyFile string, start, end int64) string {
	f, err := os.Open(filepath.Join(h.history.Dir(), "bin", bodyFile))
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, end-start))
	if err != nil {
		return ""
	}
	return string(data)
}

// StreamNotifier returns the callback the interceptor invokes as live SSE
// response bodies grow. It feeds the stream hub, which pushes incremental
// updates to subscribed detail panels. The send is blocking: the reactor
// never blocks (all per-subscriber pushes are guarded), so a dropped finalize
// must never be lost.
func (s *Server) StreamNotifier() func(entryID string, size int64, done bool) {
	return func(entryID string, size int64, done bool) {
		s.streamHub.opCh <- streamOp{kind: "notify", entryID: entryID, size: size, done: done}
	}
}

func (s *Server) handleStreamEvents(w http.ResponseWriter, r *http.Request) {
	if s.replayMode {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/streams/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	if id == "" || len(parts) < 2 || parts[1] != "events" {
		http.NotFound(w, r)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	entry, ok := s.getEntry(w, r, id)
	if !ok {
		return
	}
	if entry.Response == nil || !entry.Response.Stream {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	sub := &streamSub{entryID: id, ch: make(chan streamEvent, 64)}
	s.streamHub.opCh <- streamOp{kind: "subscribe", entryID: id, sub: sub}
	defer func() {
		// Best-effort unsubscribe: a blocked send would leak the handler
		// goroutine when the reactor is saturated.
		select {
		case s.streamHub.opCh <- streamOp{kind: "unsubscribe", entryID: id, sub: sub}:
		default:
		}
	}()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.ch:
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (s *Server) handleProcessSignature(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method == http.MethodGet {
		filePath := r.URL.Query().Get("path")
		if filePath == "" {
			http.Error(w, `{"error":"path required"}`, http.StatusBadRequest)
			return
		}
		if s.sigCache == nil {
			json.NewEncoder(w).Encode(map[string]any{"status": "unsupported"})
			return
		}
		result := s.sigCache.Get(filePath)
		if result != nil && !result.InFlight {
			json.NewEncoder(w).Encode(result)
			return
		}
		if result == nil {
			s.sigCache.VerifyAsync(filePath)
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "analyzing", "filePath": filePath})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

func (s *Server) handleProcessEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ctx := r.Context()
	ch := make(chan *proxy.SignatureResult, 16)

	if s.sigCache != nil {
		s.sigCache.OnUpdate(func(result *proxy.SignatureResult) {
			select {
			case ch <- result:
			default:
			}
		})
		for _, result := range s.sigCache.Snapshot() {
			select {
			case ch <- result:
			default:
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case result := <-ch:
			data, _ := json.Marshal(result)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// handleRecordingEvents streams the recording-stopped state to the UI. A
// snapshot of the current state is emitted on connect so the banner appears
// even when the max-duration cut happened before the page (re)loaded.
func (s *Server) handleRecordingEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ctx := r.Context()
	ch := s.recordingHub.subscribe()
	defer s.recordingHub.unsubscribe(ch)

	stopped, max := s.recordingState()
	data, _ := json.Marshal(recordingEvent{Stopped: stopped, Max: max})
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// handleReplayRuns lists every replay run stored for this session, newest first.
func (s *Server) handleReplayRuns(w http.ResponseWriter, r *http.Request) {
	if !s.replayMode {
		http.NotFound(w, r)
		return
	}
	runs, err := session.ListReplayRuns(s.replayLogDir)
	if err != nil {
		runs = nil
	}
	if runs == nil {
		runs = []session.RunSummary{}
	}
	s.writeJSON(w, map[string]any{"session": filepath.Base(s.replayLogDir), "runs": runs})
}

// handleReplayEventsList returns the events of a run. run= empty selects the
// active run.
func (s *Server) handleReplayEventsList(w http.ResponseWriter, r *http.Request) {
	if !s.replayMode {
		http.NotFound(w, r)
		return
	}
	runID := r.URL.Query().Get("run")
	if runID == "" {
		s.replayMu.Lock()
		runID = s.replayRunID
		s.replayMu.Unlock()
		if runID == "" {
			s.writeJSON(w, map[string]any{"runId": "", "events": []session.ReplayEvent{}})
			return
		}
	}
	events, err := s.replayEventsFor(runID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if events == nil {
		events = []session.ReplayEvent{}
	}

	total := len(events)
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	beforeSeq := 0
	if v := r.URL.Query().Get("beforeSeq"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			beforeSeq = n
		}
	}

	// Page the events for the lazy feed: limit serves the newest `limit`
	// events, beforeSeq restricts to events older than that seq (events are
	// ascending, so scroll-up loads use it to fetch the previous page).
	// hasMore reports whether older events exist beyond the returned page.
	// Without any param the full run is served, as before.
	page := events
	start := 0
	cut := total
	if beforeSeq > 0 {
		for i, ev := range events {
			if ev.Seq >= beforeSeq {
				cut = i
				break
			}
		}
	}
	if limit > 0 {
		start = cut - limit
		if start < 0 {
			start = 0
		}
		page = events[start:cut]
	}
	s.writeJSON(w, map[string]any{
		"runId":   runID,
		"events":  page,
		"total":   total,
		"hasMore": limit > 0 && start > 0,
	})
}

// handleReplayEventsSub dispatches the per-run endpoints: <run>/stream (SSE),
// <run>/<seq> (detail) and <run>/<seq>/body (request body bytes).
func (s *Server) handleReplayEventsSub(w http.ResponseWriter, r *http.Request) {
	if !s.replayMode {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/replay/events/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	runID := parts[0]
	if len(parts) == 2 && parts[1] == "stream" {
		s.handleReplayStream(w, r, runID)
		return
	}
	if len(parts) == 1 && parts[0] == "stream" {
		s.handleReplayStream(w, r, "")
		return
	}
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	seq, err := strconv.Atoi(parts[1])
	if err != nil || seq < 1 {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 3 && parts[2] == "body" {
		s.handleReplayBody(w, r, runID, seq)
		return
	}
	if len(parts) == 3 && parts[2] == "candidates" {
		s.handleReplayCandidates(w, r, runID, seq)
		return
	}
	if len(parts) == 4 && parts[2] == "candidates" {
		s.handleReplayCandidateDiff(w, r, runID, seq, parts[3])
		return
	}
	s.handleReplayEventDetail(w, r, runID, seq)
}

// replayDetailResponse pairs a replay event with the recorded entry it hit (if
// any), the synthetic body of a miss/exhausted response, and the match config
// the run was served under, so the frontend can render the match tab.
type replayDetailResponse struct {
	Event           session.ReplayEvent    `json:"event"`
	MatchedEntry    *history.Entry         `json:"matchedEntry,omitempty"`
	SyntheticBody   string                 `json:"syntheticBody,omitempty"`
	MatchConfig     *session.MatchConfig   `json:"matchConfig,omitempty"`
	ClientSignature *proxy.SignatureResult `json:"clientSignature,omitempty"`
}

func (s *Server) handleReplayEventDetail(w http.ResponseWriter, r *http.Request, runID string, seq int) {
	events, err := s.replayEventsFor(runID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var ev *session.ReplayEvent
	for i := range events {
		if events[i].Seq == seq {
			ev = &events[i]
			break
		}
	}
	if ev == nil {
		http.NotFound(w, r)
		return
	}
	resp := replayDetailResponse{Event: *ev, MatchConfig: s.runMatchConfig(runID)}
	resp.ClientSignature = s.resolveClientSignature(ev.ClientPath)
	if ev.Result == "hit" && ev.EntryID != "" {
		if e, err := s.hist().Get(ev.EntryID); err == nil {
			resp.MatchedEntry = e
		}
	}
	if ev.Result == "miss" {
		resp.SyntheticBody = session.MissBody(ev.Method, ev.URL)
	} else if ev.Result == "exhausted" {
		resp.SyntheticBody = session.ExhaustedBody()
	}
	s.writeJSON(w, resp)
}

// resolveClientSignature returns the origin verdict for a client path when it
// can be known without a new verification: the cached result, or the
// deterministic N/A on platforms without signature support.
func (s *Server) resolveClientSignature(clientPath string) *proxy.SignatureResult {
	if clientPath == "" || s.sigCache == nil {
		return nil
	}
	if result := s.sigCache.Get(clientPath); result != nil && !result.InFlight {
		return result
	}
	if !proxy.SignatureSupported() {
		return &proxy.SignatureResult{
			FilePath:   clientPath,
			VerifiedAt: time.Now(),
			Supported:  false,
		}
	}
	return nil
}

// handleReplayBody serves the stored raw body of a replay event's request.
func (s *Server) handleReplayBody(w http.ResponseWriter, r *http.Request, runID string, seq int) {
	events, err := s.replayEventsFor(runID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	target := r.URL.Query().Get("target")
	if target == "" {
		target = "request"
	}
	if target != "request" && target != "served" {
		http.Error(w, "invalid target", http.StatusBadRequest)
		return
	}
	var ev *session.ReplayEvent
	for i := range events {
		if events[i].Seq == seq {
			e := events[i]
			ev = &e
			break
		}
	}
	if ev == nil {
		http.NotFound(w, r)
		return
	}
	var bodyFile string
	var headers map[string][]string
	var isBinary bool
	if target == "served" {
		if ev.ServedResponse == nil || ev.ServedResponse.BodyFile == "" {
			http.NotFound(w, r)
			return
		}
		bodyFile = ev.ServedResponse.BodyFile
		headers = ev.ServedResponse.Headers
		isBinary = ev.ServedResponse.IsBinaryBody
	} else {
		if ev.Request.BodyFile == "" {
			http.NotFound(w, r)
			return
		}
		bodyFile = ev.Request.BodyFile
		headers = ev.Request.Headers
		isBinary = ev.Request.IsBinaryBody
	}
	dir, err := session.ReplayRunDir(s.replayLogDir, runID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, err := session.ReadReplayBody(dir, bodyFile)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := http.Header(headers)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	switch {
	case isBinary:
		w.Header().Set("Content-Type", "application/octet-stream")
	case h.Get("Content-Type") != "":
		w.Header().Set("Content-Type", h.Get("Content-Type"))
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Write(data)
}

// handleReplayStream streams replay events over SSE. With a specific runID (the
// legacy per-run path <runID>/stream) only that run can stream and a run switch
// closes the connection. With runID=="" (the active stream /api/replay/events/
// stream) it subscribes regardless of whether a run is active, sends a leading
// runChanged announcing the current run, then streams its snapshot and keeps
// streaming across run switches - the frontend uses this so the panel never has
// to reconnect.
func (s *Server) handleReplayStream(w http.ResponseWriter, r *http.Request, runID string) {
	s.replayMu.Lock()
	if runID != "" && runID != s.replayRunID {
		s.replayMu.Unlock()
		http.NotFound(w, r)
		return
	}
	ch := make(chan session.ReplayEvent, 64)
	s.replayClients[ch] = struct{}{}
	snapshot := append([]session.ReplayEvent(nil), s.replayEvents...)
	snapshotRun := s.replayRunID
	s.replayMu.Unlock()
	defer func() {
		s.replayMu.Lock()
		delete(s.replayClients, ch)
		s.replayMu.Unlock()
	}()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	active := runID == ""
	current := runID
	if active {
		current = snapshotRun
		data, _ := json.Marshal(map[string]string{"type": "runChanged", "runId": current})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
	for _, ev := range snapshot {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	if len(snapshot) > 0 {
		flusher.Flush()
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			if ev.RunID != current {
				current = ev.RunID
				data, _ := json.Marshal(map[string]string{"type": "runChanged", "runId": current})
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
				if !active {
					return
				}
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
