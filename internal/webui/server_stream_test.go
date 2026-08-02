package webui

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gospy/internal/history"
)

type streamEventMsg struct {
	Type      string `json:"type"`
	BodySize  int64  `json:"bodySize,omitempty"`
	Stream    bool   `json:"stream"`
	Truncated bool   `json:"truncated,omitempty"`
	Preview   string `json:"preview,omitempty"`
}

// readSSE reads one SSE frame (a `data:` line terminated by a blank line).
func readSSE(r *bufio.Reader) (string, error) {
	var data strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if data.Len() > 0 {
				return data.String(), nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
}

func newStreamEntry(t *testing.T, hist *history.Store, id, content string) string {
	t.Helper()
	bodyFile := id + "-stream.bin"
	binDir := filepath.Join(hist.Dir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, bodyFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := &history.Entry{
		ID:      id,
		Request: history.RequestRecord{Method: "GET", URL: "http://a.com/events", Host: "a.com"},
		Response: &history.ResponseRecord{
			Status:   200,
			Headers:  map[string][]string{"Content-Type": {"text/event-stream"}},
			BodyFile: bodyFile,
			BodySize: int64(len(content)),
			Stream:   true,
		},
	}
	if err := hist.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return bodyFile
}

// TestStreamEvents_SSE_Flow drives the full stream event lifecycle over the
// real SSE endpoint: snapshot with the initial capture, incremental deltas as
// the body grows, and the final done event.
func TestStreamEvents_SSE_Flow(t *testing.T) {
	s, _, hist := newTestServer(t)

	const id = "entry-stream-1"
	newStreamEntry(t, hist, id, "data: one\n\n")

	ts := httptest.NewServer(http.HandlerFunc(s.handleStreamEvents))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/streams/" + id + "/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	r := bufio.NewReader(resp.Body)

	// snapshot: initial preview + size
	raw, err := readSSE(r)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var snap streamEventMsg
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatalf("decode snapshot %q: %v", raw, err)
	}
	if snap.Type != "snapshot" || !snap.Stream || snap.BodySize != 11 {
		t.Errorf("snapshot = %+v", snap)
	}
	if snap.Preview != "data: one\n\n" {
		t.Errorf("snapshot preview = %q", snap.Preview)
	}

	// delta: append content, notify, expect the incremental update
	binDir := filepath.Join(hist.Dir(), "bin")
	full := "data: one\n\ndata: two\n\n"
	if err := os.WriteFile(filepath.Join(binDir, id+"-stream.bin"), []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
	s.StreamNotifier()(id, int64(len(full)), false)
	raw, err = readSSE(r)
	if err != nil {
		t.Fatalf("read update: %v", err)
	}
	var upd streamEventMsg
	if err := json.Unmarshal([]byte(raw), &upd); err != nil {
		t.Fatalf("decode update %q: %v", raw, err)
	}
	if upd.Type != "update" || !upd.Stream || upd.BodySize != int64(len(full)) || upd.Preview != "data: two\n\n" {
		t.Errorf("update = %+v", upd)
	}

	// done: final event with stream=false
	s.StreamNotifier()(id, int64(len(full)), true)
	raw, err = readSSE(r)
	if err != nil {
		t.Fatalf("read done: %v", err)
	}
	var done streamEventMsg
	if err := json.Unmarshal([]byte(raw), &done); err != nil {
		t.Fatalf("decode done %q: %v", raw, err)
	}
	if done.Type != "done" || done.Stream || done.BodySize != int64(len(full)) {
		t.Errorf("done = %+v", done)
	}
}

// TestStreamEvents_SnapshotTruncated verifies the view cap: when the capture
// exceeds the preview limit, the snapshot carries truncated=true and only the
// first cap bytes, and later deltas stop once the cap is reached.
func TestStreamEvents_SnapshotTruncated(t *testing.T) {
	s, _, hist := newTestServer(t)
	s.streamHub.maxPreview = 10

	const id = "entry-stream-trunc"
	newStreamEntry(t, hist, id, "0123456789abcdefghij") // 20 bytes

	ts := httptest.NewServer(http.HandlerFunc(s.handleStreamEvents))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/streams/" + id + "/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)

	raw, err := readSSE(r)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var snap streamEventMsg
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatalf("decode snapshot %q: %v", raw, err)
	}
	if snap.Type != "snapshot" || !snap.Truncated || snap.BodySize != 20 || snap.Preview != "0123456789" {
		t.Errorf("snapshot = %+v", snap)
	}
}

// TestDetail_Preview_StreamingBody verifies the detail endpoint serves the
// captured preview for a stream body (text file), with the truncation marker
// when the capture exceeds the view limit, and no body file loading for
// already-inline bodies.
func TestDetail_Preview_StreamingBody(t *testing.T) {
	s, _, hist := newTestServer(t)

	const id = "entry-stream-detail"
	content := strings.Repeat("data: x\n\n", 20) // 160 bytes
	newStreamEntry(t, hist, id, content)

	req := httptest.NewRequest("GET", "/api/requests/"+id, nil)
	w := httptest.NewRecorder()
	s.handleGetRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got history.Entry
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Response == nil || got.Response.Body != content {
		t.Errorf("Response.Body = %q, want full preview", got.Response.Body)
	}
	if !got.Response.Stream {
		t.Error("Response.Stream = false, want true")
	}
	if got.Response.BodySize != int64(len(content)) {
		t.Errorf("BodySize = %d, want %d", got.Response.BodySize, len(content))
	}
}

// TestDetail_Preview_TruncationMarker verifies a capture larger than the view
// limit is served truncated with the marker.
func TestDetail_Preview_TruncationMarker(t *testing.T) {
	s, _, hist := newTestServer(t)

	const id = "entry-stream-huge"
	// Build a body over the 2MB limit cheaply: a single repeating chunk.
	huge := strings.Repeat("0123456789abcdef", maxBodyLen/16+16) // over 2MB
	newStreamEntry(t, hist, id, huge)

	req := httptest.NewRequest("GET", "/api/requests/"+id, nil)
	w := httptest.NewRecorder()
	s.handleGetRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got history.Entry
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Response == nil || got.Response.Body == "" {
		t.Fatal("Response.Body empty")
	}
	if len(got.Response.Body) != maxBodyLen+len("\n... [truncated - body too large]") {
		t.Errorf("preview len = %d, want %d (cap + marker)", len(got.Response.Body), maxBodyLen+len("\n... [truncated - body too large]"))
	}
	if !strings.Contains(got.Response.Body, "[truncated - body too large]") {
		t.Error("expected truncation marker in preview")
	}
	if got.Response.BodySize != int64(len(huge)) {
		t.Errorf("BodySize = %d, want %d (full size preserved)", got.Response.BodySize, len(huge))
	}
}

// TestBodyBin_StreamsLargeBody verifies body-bin serves the full capture (even
// far beyond the view limit) without truncation.
func TestBodyBin_StreamsLargeBody(t *testing.T) {
	s, _, hist := newTestServer(t)

	const id = "entry-stream-bin"
	huge := strings.Repeat("0123456789abcdef", maxBodyLen/16+16)
	newStreamEntry(t, hist, id, huge)

	req := httptest.NewRequest("GET", "/api/requests/"+id+"/body-bin?target=response", nil)
	w := httptest.NewRecorder()
	s.handleGetRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if w.Body.Len() != len(huge) {
		t.Errorf("body len = %d, want %d (full body, not truncated)", w.Body.Len(), len(huge))
	}
	if w.Body.String() != huge {
		t.Errorf("body content mismatch")
	}
}
