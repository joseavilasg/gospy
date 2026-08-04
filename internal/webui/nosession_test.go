package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gospy/internal/rules"
)

// newNoSessionServer builds a Server holding no session store - the state of
// record auto mode before the first POST /api/session/start.
func newNoSessionServer(t *testing.T) *Server {
	t.Helper()
	rulesPath := t.TempDir() + "/rules.json"
	rulesStore := rules.NewStore(rulesPath)
	if err := rulesStore.Load(); err != nil {
		t.Fatalf("rulesStore.Load: %v", err)
	}
	engine := rules.NewEngine()
	return NewServer(":0", nil, newMockIgnoreChecker(), newMockFocusChecker(), rulesStore, engine, ":8080", nil, nil, NewFilterStore(t.TempDir()+"/filters.json"))
}

func TestNoSessionServerConstructs(t *testing.T) {
	s := newNoSessionServer(t)
	if s.hist() != nil {
		t.Fatal("hist() should be nil before a session starts")
	}
}

func TestNoSessionListEmpty(t *testing.T) {
	s := newNoSessionServer(t)
	rec := httptest.NewRecorder()
	s.handleListRequests(rec, httptest.NewRequest(http.MethodGet, "/api/requests", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var lr listResponse
	if err := json.NewDecoder(rec.Body).Decode(&lr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if lr.Total != 0 {
		t.Errorf("Total = %d, want 0", lr.Total)
	}
	if len(lr.Entries) != 0 {
		t.Errorf("len(Entries) = %d, want 0", len(lr.Entries))
	}
}

func TestNoSessionListDiffEmpty(t *testing.T) {
	s := newNoSessionServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/requests?since=2026-01-01T00:00:00Z&version=1", nil)
	rec := httptest.NewRecorder()
	s.handleListRequests(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var lr listResponse
	if err := json.NewDecoder(rec.Body).Decode(&lr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(lr.Upserts) != 0 {
		t.Errorf("len(Upserts) = %d, want 0", len(lr.Upserts))
	}
}

func TestNoSessionDetailNotFound(t *testing.T) {
	s := newNoSessionServer(t)
	rec := httptest.NewRecorder()
	s.handleGetRequest(rec, httptest.NewRequest(http.MethodGet, "/api/requests/e1", nil))
	assertStatus(t, "get entry", rec, http.StatusNotFound)
}

func TestNoSessionWritesNotFound(t *testing.T) {
	s := newNoSessionServer(t)
	cases := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request, string)
		body    string
	}{
		{"save body", s.handleSaveBody, `{"target":"request","body":"x"}`},
		{"revert body", s.handleRevertBody, ""},
		{"save headers", s.handleSaveHeaders, `{"headers":{}}`},
		{"revert headers", s.handleRevertHeaders, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			var req *http.Request
			if c.body == "" {
				req = httptest.NewRequest(http.MethodDelete, "/api/requests/e1/body", nil)
			} else {
				req = httptest.NewRequest(http.MethodPut, "/api/requests/e1/body", strings.NewReader(c.body))
			}
			c.handler(rec, req, "e1")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
		})
	}
}
