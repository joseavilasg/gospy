package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"gospy/internal/history"
)

func TestSessionStartEndpointNoStarter(t *testing.T) {
	s, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/session/start", nil)
	rec := httptest.NewRecorder()
	s.handleSessionStart(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no session recording active") {
		t.Errorf("body = %q, want the no-starter error", rec.Body.String())
	}
}

func TestSessionStartEndpointWithStarter(t *testing.T) {
	s, _, _ := newTestServer(t)
	s.SetSessionStarter(func(name string) (string, string, error) {
		return "/tmp/sessions/" + name, name, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/api/session/start", strings.NewReader(`{"name":"cap1"}`))
	rec := httptest.NewRecorder()
	s.handleSessionStart(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["session"] != "/tmp/sessions/cap1" || out["name"] != "cap1" {
		t.Errorf("response = %v", out)
	}
}

func TestSessionStartEndpointAutoName(t *testing.T) {
	s, _, _ := newTestServer(t)
	s.SetSessionStarter(func(name string) (string, string, error) {
		return "/tmp/sessions/auto-" + name, "auto-" + name, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/api/session/start", nil)
	rec := httptest.NewRecorder()
	s.handleSessionStart(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestSessionStartEndpointMethodNotAllowed(t *testing.T) {
	s, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/session/start", nil)
	rec := httptest.NewRecorder()
	s.handleSessionStart(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("code = %d, want 405", rec.Code)
	}
}

func TestSetHistoryStoreRotation(t *testing.T) {
	s, _, _ := newTestServer(t)
	if err := s.hist().Save(&history.Entry{ID: "a"}); err != nil {
		t.Fatalf("Save a: %v", err)
	}
	if err := s.hist().Save(&history.Entry{ID: "b"}); err != nil {
		t.Fatalf("Save b: %v", err)
	}

	getList := func(version int) (total, v int, entries int) {
		url := "/api/requests"
		if version >= 0 {
			url += "?since=" + time.Now().Add(-time.Hour).Format(time.RFC3339Nano) + "&version=" + strconv.Itoa(version)
		}
		req := httptest.NewRequest(http.MethodGet, url, nil)
		rec := httptest.NewRecorder()
		s.handleListRequests(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d: %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Entries []*history.ListEntry `json:"entries"`
			Total   int                  `json:"total"`
			Version int                  `json:"version"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return out.Total, out.Version, len(out.Entries)
	}

	total, v1, _ := getList(-1)
	if total != 2 {
		t.Fatalf("total before rotation = %d, want 2", total)
	}

	hist2, err := history.New(t.TempDir() + "/history")
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}
	s.SetHistoryStore(hist2)

	// version must be bumped (Touch) so the client's stale version forces a
	// full refetch, and the empty rotated store must yield total 0.
	total, v2, entries := getList(v1)
	if total != 0 {
		t.Errorf("total after rotation = %d, want 0", total)
	}
	if entries != 0 {
		t.Errorf("entries after rotation = %d, want 0", entries)
	}
	if v2 <= v1 {
		t.Errorf("version after rotation = %d, want > %d", v2, v1)
	}
}
