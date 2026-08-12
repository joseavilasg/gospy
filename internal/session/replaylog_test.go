package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gospy/internal/history"
)

func TestReplayLogAppendAndLoad(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenReplayLog(dir, "run-1")
	if err != nil {
		t.Fatalf("OpenReplayLog: %v", err)
	}

	ev1 := ReplayEvent{
		RunID:     "run-1",
		Timestamp: time.Now(),
		Method:    "GET",
		URL:       "https://example.com/a",
		Result:    "hit",
		Status:    200,
		EntryID:   "entry-1",
		Request:   history.RequestRecord{Method: "GET", URL: "https://example.com/a", Host: "example.com"},
	}
	if err := log.Append(&ev1, []byte("raw body"), nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if ev1.Seq != 1 {
		t.Fatalf("expected seq 1, got %d", ev1.Seq)
	}
	if ev1.Request.BodyFile != "1.req.bin" {
		t.Fatalf("expected BodyFile 1.req.bin, got %q", ev1.Request.BodyFile)
	}

	ev2 := ReplayEvent{
		RunID:     "run-1",
		Timestamp: time.Now(),
		Method:    "GET",
		URL:       "https://example.com/b",
		Result:    "miss",
		Status:    404,
		Request:   history.RequestRecord{Method: "GET", URL: "https://example.com/b", Host: "example.com"},
	}
	if err := log.Append(&ev2, nil, nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if ev2.Seq != 2 {
		t.Fatalf("expected seq 2, got %d", ev2.Seq)
	}
	if ev2.Request.BodyFile != "" {
		t.Fatalf("expected no body file, got %q", ev2.Request.BodyFile)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events, err := LoadReplayRun(dir)
	if err != nil {
		t.Fatalf("LoadReplayRun: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Seq != 1 || events[0].Result != "hit" || events[0].EntryID != "entry-1" {
		t.Fatalf("unexpected event[0]: %+v", events[0])
	}
	if events[1].Seq != 2 || events[1].Result != "miss" {
		t.Fatalf("unexpected event[1]: %+v", events[1])
	}

	raw, err := ReadReplayBody(dir, "1.req.bin")
	if err != nil {
		t.Fatalf("ReadReplayBody: %v", err)
	}
	if string(raw) != "raw body" {
		t.Fatalf("unexpected body %q", raw)
	}
}

func TestReplayLogBodyFilePersistsInRequest(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenReplayLog(dir, "run-2")
	if err != nil {
		t.Fatalf("OpenReplayLog: %v", err)
	}
	ev := ReplayEvent{
		RunID:  "run-2",
		Method: "POST",
		URL:    "https://example.com/report",
		Result: "miss",
		Status: 404,
		Request: history.RequestRecord{
			Method:  "POST",
			URL:     "https://example.com/report",
			Host:    "example.com",
			Headers: map[string][]string{"Content-Type": {"application/json"}},
			Body:    `{"ok":true}`,
			RawBody: `{"ok":true}`,
		},
	}
	if err := log.Append(&ev, []byte(`{"ok":true}`), nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	log.Close()

	events, err := LoadReplayRun(dir)
	if err != nil {
		t.Fatalf("LoadReplayRun: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	got := events[0]
	if got.Request.BodyFile != "1.req.bin" {
		t.Fatalf("expected BodyFile 1.req.bin, got %q", got.Request.BodyFile)
	}
	if got.Request.Body != `{"ok":true}` {
		t.Fatalf("expected decoded body, got %q", got.Request.Body)
	}
}

func TestReplayLogListRuns(t *testing.T) {
	root := t.TempDir()

	mk := func(runID string, results ...string) {
		t.Helper()
		dir := filepath.Join(root, runID)
		log, err := OpenReplayLog(dir, runID)
		if err != nil {
			t.Fatalf("OpenReplayLog: %v", err)
		}
		for i, res := range results {
			status := 200
			switch res {
			case "miss":
				status = 404
			case "exhausted":
				status = 410
			}
			ev := ReplayEvent{
				RunID:     runID,
				Method:    "GET",
				URL:       "https://example.com/" + runID + "/" + string(rune('a'+i)),
				Result:    res,
				Status:    status,
				Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			}
			if err := log.Append(&ev, nil, nil); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		log.Close()
	}

	mk("20260803-100000", "hit", "hit", "miss")
	mk("20260803-110000", "hit", "miss", "exhausted", "exhausted")

	summaries, err := ListReplayRuns(root)
	if err != nil {
		t.Fatalf("ListReplayRuns: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(summaries))
	}
	// newest first
	if summaries[0].RunID != "20260803-110000" {
		t.Fatalf("expected newest run first, got %q", summaries[0].RunID)
	}
	first := summaries[0]
	if first.Hits != 1 || first.Misses != 1 || first.Exhausted != 2 {
		t.Fatalf("unexpected summary %+v", first)
	}
	second := summaries[1]
	if second.Hits != 2 || second.Misses != 1 || second.Exhausted != 0 {
		t.Fatalf("unexpected summary %+v", second)
	}
}

func TestReplayRunDirRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{"", "..", "../evil", "a/b", "a\\b", "/abs"} {
		if _, err := ReplayRunDir(root, bad); err == nil {
			t.Fatalf("expected error for run id %q", bad)
		}
	}
	dir, err := ReplayRunDir(root, "20260803-100000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != filepath.Join(root, "20260803-100000") {
		t.Fatalf("unexpected dir %q", dir)
	}
}

func TestNextRunDirCollision(t *testing.T) {
	root := t.TempDir()
	first := nextRunDir(root)
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// The next run dir must be different (collision resolved or timestamp
	// advanced).
	second := nextRunDir(root)
	if second == first {
		t.Fatal("expected a different run dir on collision")
	}
}
