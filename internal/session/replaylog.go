package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gospy/internal/history"
)

// UnconsumedEntry is a recorded entry that was still available in the replay
// queue when a request failed to match. Only the id is persisted - the pending
// set is scoped into the request list by id, the run log does not need to
// repeat the full entry.
type UnconsumedEntry struct {
	ID string `json:"id,omitempty"`
}

// ReplayEvent records one request as it reached the replay server: the
// incoming request in full (history body model), the match outcome, and - for
// misses - the queue context at that moment.
type ReplayEvent struct {
	Seq          int                   `json:"seq"`
	RunID        string                `json:"runId"`
	Timestamp    time.Time             `json:"ts"`
	Method       string                `json:"method"`
	URL          string                `json:"url"`
	Result       string                `json:"result"` // hit | miss | exhausted
	Status       int                   `json:"status"`
	EntryID      string                `json:"entryId,omitempty"`
	MatchedURL   string                `json:"matchedUrl,omitempty"`
	Request      history.RequestRecord `json:"request"`
	Unconsumed   []UnconsumedEntry     `json:"unconsumed,omitempty"`
	TotalPending int                   `json:"totalPending,omitempty"`
	Consumed     int                   `json:"consumed"`
	Total        int                   `json:"total"`
	Exhausted    bool                  `json:"exhausted"`
}

// ReplayLog persists the events of a single replay run as JSONL under
// <dir>/events.jsonl, with request bodies (always raw, wire fidelity) in
// <dir>/bin/<seq>.req.bin. Text bodies additionally carry the decoded copy
// inline, mirroring the history body model.
type ReplayLog struct {
	mu     sync.Mutex
	dir    string
	binDir string
	runID  string
	f      *os.File
	seq    int
}

func OpenReplayLog(runDir, runID string) (*ReplayLog, error) {
	binDir := filepath.Join(runDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(runDir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &ReplayLog{dir: runDir, binDir: binDir, runID: runID, f: f}, nil
}

func (l *ReplayLog) RunID() string { return l.runID }
func (l *ReplayLog) Dir() string   { return l.dir }

// Append assigns the next sequence number, persists the raw request body when
// present, and appends the event. The write lands in the OS buffer (survives
// process death); the file is synced on Close.
func (l *ReplayLog) Append(ev *ReplayEvent, rawBody []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	ev.Seq = l.seq
	if len(rawBody) > 0 {
		name := fmt.Sprintf("%d.req.bin", l.seq)
		if err := os.WriteFile(filepath.Join(l.binDir, name), rawBody, 0o644); err != nil {
			return err
		}
		ev.Request.BodyFile = name
		ev.Request.BodySize = int64(len(rawBody))
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := l.f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func (l *ReplayLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Sync()
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	l.f = nil
	return err
}

// RunSummary is a compact description of a replay run for the run browser.
type RunSummary struct {
	RunID      string    `json:"runId"`
	Timestamp  time.Time `json:"ts"`
	Hits       int       `json:"hits"`
	Misses     int       `json:"misses"`
	Exhausted  int       `json:"exhausted"`
	DurationMs int64     `json:"durationMs"`
}

// ListReplayRuns summarizes every replay run stored under root, newest first.
func ListReplayRuns(root string) ([]RunSummary, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var summaries []RunSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		events, err := LoadReplayRun(filepath.Join(root, e.Name()))
		if err != nil || len(events) == 0 {
			continue
		}
		summaries = append(summaries, summarizeRun(events))
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].RunID > summaries[j].RunID })
	return summaries, nil
}

func summarizeRun(events []ReplayEvent) RunSummary {
	s := RunSummary{RunID: events[0].RunID, Timestamp: events[0].Timestamp}
	for _, ev := range events {
		switch ev.Result {
		case "hit":
			s.Hits++
		case "miss":
			s.Misses++
		case "exhausted":
			s.Exhausted++
		}
	}
	if len(events) > 1 {
		s.DurationMs = events[len(events)-1].Timestamp.Sub(events[0].Timestamp).Milliseconds()
	}
	return s
}

// LoadReplayRun reads every event of a replay run directory.
func LoadReplayRun(runDir string) ([]ReplayEvent, error) {
	data, err := os.ReadFile(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		return nil, err
	}
	var events []ReplayEvent
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev ReplayEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		events = append(events, ev)
	}
	return events, nil
}

// ReadReplayBody reads a stored request body file from a replay run directory.
func ReadReplayBody(runDir, bodyFile string) ([]byte, error) {
	return os.ReadFile(filepath.Join(runDir, "bin", filepath.Base(bodyFile)))
}

// ReplayRunDir returns the directory of a replay run, refusing path traversal
// outside root.
func ReplayRunDir(root, runID string) (string, error) {
	if runID == "" || runID == "." || runID == ".." || runID != filepath.Base(runID) || strings.ContainsAny(runID, `/\`) {
		return "", fmt.Errorf("invalid run id %q", runID)
	}
	return filepath.Join(root, runID), nil
}
