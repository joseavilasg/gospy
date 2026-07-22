package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gospy/internal/history"
	"gospy/internal/rules"
)

type ruleResponse struct {
	Rule        rules.Rule `json:"rule"`
	Deactivated []string   `json:"deactivated"`
}

func newTestServer(t *testing.T) (*Server, *rules.Store, *history.Store) {
	t.Helper()
	hist, err := history.New(t.TempDir() + "/history")
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}
	rulesPath := t.TempDir() + "/rules.json"
	rulesStore := rules.NewStore(rulesPath)
	if err := rulesStore.Load(); err != nil {
		t.Fatalf("rulesStore.Load: %v", err)
	}
	engine := rules.NewEngine()

	ignoreStore := newMockIgnoreChecker()
	focusStore := newMockFocusChecker()

	s := NewServer(":0", hist, ignoreStore, focusStore, rulesStore, engine, nil, nil)
	return s, rulesStore, hist
}

type mockIgnoreChecker struct {
	hosts map[string]bool
}

func newMockIgnoreChecker() *mockIgnoreChecker {
	return &mockIgnoreChecker{hosts: make(map[string]bool)}
}

func (m *mockIgnoreChecker) IsIgnored(host string) bool { return m.hosts[host] }
func (m *mockIgnoreChecker) Matches(host string) bool   { return m.hosts[host] }
func (m *mockIgnoreChecker) List() []string             { return nil }
func (m *mockIgnoreChecker) Add(host string) error      { m.hosts[host] = true; return nil }
func (m *mockIgnoreChecker) Remove(host string) error   { delete(m.hosts, host); return nil }

type mockFocusChecker struct{}

func newMockFocusChecker() *mockFocusChecker { return &mockFocusChecker{} }

func (m *mockFocusChecker) IsFocused(host string) bool { return false }
func (m *mockFocusChecker) Matches(host string) bool   { return false }
func (m *mockFocusChecker) List() []string             { return nil }
func (m *mockFocusChecker) Add(host string) error      { return nil }
func (m *mockFocusChecker) Remove(host string) error   { return nil }

func TestHandleRules_GET_Empty(t *testing.T) {
	s, _, _ := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/rules", nil)
	w := httptest.NewRecorder()
	s.handleRules(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /api/rules status = %d, want %d", w.Code, http.StatusOK)
	}

	var result []*rules.Rule
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("GET /api/rules returned %d rules, want 0", len(result))
	}
}

func TestHandleRules_POST_Create(t *testing.T) {
	s, _, _ := newTestServer(t)

	ruleBody := rules.Rule{
		Name:    "Mock API",
		Match:   rules.MatchRule{Method: "GET", Host: "api.example.com"},
		Action:  rules.ActionMock,
		Enabled: true,
		MockResp: &rules.MockResponse{
			Status: 200,
			Body:   `{"mocked":true}`,
		},
	}
	body, _ := json.Marshal(ruleBody)

	req := httptest.NewRequest("POST", "/api/rules", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleRules(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("POST /api/rules status = %d, want %d", w.Code, http.StatusCreated)
	}

	var result struct {
		Rule        rules.Rule `json:"rule"`
		Deactivated []string   `json:"deactivated"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Rule.ID == "" {
		t.Error("created rule should have ID")
	}
	if result.Rule.Name != "Mock API" {
		t.Errorf("Name = %q, want %q", result.Rule.Name, "Mock API")
	}
	if result.Rule.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestHandleRules_POST然後_GET(t *testing.T) {
	s, _, _ := newTestServer(t)

	// Create a rule
	ruleBody := rules.Rule{
		Name:   "Block telemetry",
		Match:  rules.MatchRule{Host: "telemetry.example.com"},
		Action: rules.ActionDrop,
	}
	body, _ := json.Marshal(ruleBody)
	createReq := httptest.NewRequest("POST", "/api/rules", bytes.NewReader(body))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	s.handleRules(createW, createReq)

	// List rules
	listReq := httptest.NewRequest("GET", "/api/rules", nil)
	listW := httptest.NewRecorder()
	s.handleRules(listW, listReq)

	var result []*rules.Rule
	json.NewDecoder(listW.Body).Decode(&result)
	if len(result) != 1 {
		t.Fatalf("GET /api/rules after create = %d, want 1", len(result))
	}
	if result[0].Name != "Block telemetry" {
		t.Errorf("Name = %q, want %q", result[0].Name, "Block telemetry")
	}
}

func TestHandleRule_PUT_Update(t *testing.T) {
	s, _, _ := newTestServer(t)

	// Create
	ruleBody := rules.Rule{Name: "Original", Match: rules.MatchRule{Method: "GET"}, Action: rules.ActionMock, MockResp: &rules.MockResponse{Status: 200}}
	body, _ := json.Marshal(ruleBody)
	createReq := httptest.NewRequest("POST", "/api/rules", bytes.NewReader(body))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	s.handleRules(createW, createReq)

	var createdResp ruleResponse
	json.NewDecoder(createW.Body).Decode(&createdResp)
	created := &createdResp.Rule

	// Update
	updatedBody := rules.Rule{Name: "Updated", Match: rules.MatchRule{Method: "POST"}, Action: rules.ActionDrop, Enabled: true}
	body, _ = json.Marshal(updatedBody)
	updateReq := httptest.NewRequest("PUT", "/api/rules/"+created.ID, bytes.NewReader(body))
	updateReq.Header.Set("Content-Type", "application/json")
	updateW := httptest.NewRecorder()
	s.handleRule(updateW, updateReq)

	if updateW.Code != http.StatusOK {
		t.Errorf("PUT status = %d, want %d", updateW.Code, http.StatusOK)
	}

	var updated rules.Rule
	json.NewDecoder(updateW.Body).Decode(&updated)
	if updated.Name != "Updated" {
		t.Errorf("Name after update = %q, want %q", updated.Name, "Updated")
	}
	if updated.Action != rules.ActionDrop {
		t.Errorf("Action after update = %q, want %q", updated.Action, rules.ActionDrop)
	}
}

func TestHandleRule_DELETE(t *testing.T) {
	s, _, _ := newTestServer(t)

	// Create
	ruleBody := rules.Rule{Name: "To Delete", Match: rules.MatchRule{Method: "GET"}, Action: rules.ActionMock, MockResp: &rules.MockResponse{Status: 200}}
	body, _ := json.Marshal(ruleBody)
	createReq := httptest.NewRequest("POST", "/api/rules", bytes.NewReader(body))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	s.handleRules(createW, createReq)

	var createdResp ruleResponse
	json.NewDecoder(createW.Body).Decode(&createdResp)
	created := &createdResp.Rule

	// Delete
	deleteReq := httptest.NewRequest("DELETE", "/api/rules/"+created.ID, nil)
	deleteW := httptest.NewRecorder()
	s.handleRule(deleteW, deleteReq)

	if deleteW.Code != http.StatusOK {
		t.Errorf("DELETE status = %d, want %d", deleteW.Code, http.StatusOK)
	}

	// Verify gone
	listReq := httptest.NewRequest("GET", "/api/rules", nil)
	listW := httptest.NewRecorder()
	s.handleRules(listW, listReq)

	var result []*rules.Rule
	json.NewDecoder(listW.Body).Decode(&result)
	if len(result) != 0 {
		t.Errorf("GET after delete = %d rules, want 0", len(result))
	}
}

func TestHandleRule_PATCH_Toggle(t *testing.T) {
	s, _, _ := newTestServer(t)

	// Create enabled rule
	ruleBody := rules.Rule{Name: "Toggle Me", Match: rules.MatchRule{Method: "GET"}, Action: rules.ActionMock, Enabled: true, MockResp: &rules.MockResponse{Status: 200}}
	body, _ := json.Marshal(ruleBody)
	createReq := httptest.NewRequest("POST", "/api/rules", bytes.NewReader(body))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	s.handleRules(createW, createReq)

	var createdResp ruleResponse
	json.NewDecoder(createW.Body).Decode(&createdResp)
	created := &createdResp.Rule

	if !created.Enabled {
		t.Fatal("created rule should be enabled")
	}

	// Toggle off
	toggleReq := httptest.NewRequest("PATCH", "/api/rules/"+created.ID, nil)
	toggleW := httptest.NewRecorder()
	s.handleRule(toggleW, toggleReq)

	var toggleResult ruleResponse
	json.NewDecoder(toggleW.Body).Decode(&toggleResult)
	if toggleResult.Rule.Enabled {
		t.Error("after toggle, Enabled should be false")
	}

	// Toggle back on
	toggleReq2 := httptest.NewRequest("PATCH", "/api/rules/"+created.ID, nil)
	toggleW2 := httptest.NewRecorder()
	s.handleRule(toggleW2, toggleReq2)

	var toggleResult2 ruleResponse
	json.NewDecoder(toggleW2.Body).Decode(&toggleResult2)
	if !toggleResult2.Rule.Enabled {
		t.Error("after second toggle, Enabled should be true")
	}
}

func TestHandleRules_POST_InvalidBody(t *testing.T) {
	s, _, _ := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/rules", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleRules(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("POST invalid body status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRules_MethodNotAllowed(t *testing.T) {
	s, _, _ := newTestServer(t)

	req := httptest.NewRequest("DELETE", "/api/rules", nil)
	w := httptest.NewRecorder()
	s.handleRules(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /api/rules status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleRule_Delete_NotFound(t *testing.T) {
	s, _, _ := newTestServer(t)

	req := httptest.NewRequest("DELETE", "/api/rules/nonexistent", nil)
	w := httptest.NewRecorder()
	s.handleRule(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("DELETE /api/rules/nonexistent status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleRule_MethodNotAllowed(t *testing.T) {
	s, _, _ := newTestServer(t)

	req := httptest.NewRequest("OPTIONS", "/api/rules/someid", nil)
	w := httptest.NewRecorder()
	s.handleRule(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("OPTIONS /api/rules/someid status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleRequestRule(t *testing.T) {
	s, _, hist := newTestServer(t)

	// Save an entry
	entry := &history.Entry{
		Request: history.RequestRecord{
			Method:  "POST",
			URL:     "http://api.example.com/data",
			Host:    "api.example.com",
			Headers: map[string][]string{"Content-Type": {"application/json"}},
			Body:    `{"key":"value"}`,
		},
		AppliedAction: "passthrough",
	}
	if err := hist.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/request-rule?id="+entry.ID, nil)
	w := httptest.NewRecorder()
	s.handleRequestRule(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /api/request-rule status = %d, want %d", w.Code, http.StatusOK)
	}

	var result history.Entry
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Request.Method != "POST" {
		t.Errorf("Method = %q, want %q", result.Request.Method, "POST")
	}
	if result.Request.Host != "api.example.com" {
		t.Errorf("Host = %q, want %q", result.Request.Host, "api.example.com")
	}
}

func TestHandleRequestRule_MissingID(t *testing.T) {
	s, _, _ := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/request-rule", nil)
	w := httptest.NewRecorder()
	s.handleRequestRule(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("GET /api/request-rule without id status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestEngineReloadAfterCRUD(t *testing.T) {
	s, rulesStore, _ := newTestServer(t)

	// Create
	ruleBody := rules.Rule{
		Name:     "Test rule",
		Match:    rules.MatchRule{Method: "GET", Host: "example.com"},
		Action:   rules.ActionMock,
		Enabled:  true,
		MockResp: &rules.MockResponse{Status: 418, Body: `{"tea":true}`},
	}
	body, _ := json.Marshal(ruleBody)
	createReq := httptest.NewRequest("POST", "/api/rules", bytes.NewReader(body))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	s.handleRules(createW, createReq)

	// Engine should have the rule
	rulesList := rulesStore.GetRules()
	if len(rulesList) != 1 {
		t.Fatalf("rulesStore.GetRules() = %d, want 1", len(rulesList))
	}

	match := s.engine.Match("GET", "example.com", "http://example.com/api", nil)
	if match == nil {
		t.Error("engine.Match should find the rule after POST")
	}

	// Delete
	var createdResp ruleResponse
	json.NewDecoder(createW.Body).Decode(&createdResp)
	created := &createdResp.Rule
	deleteReq := httptest.NewRequest("DELETE", "/api/rules/"+created.ID, nil)
	deleteW := httptest.NewRecorder()
	s.handleRule(deleteW, deleteReq)

	match2 := s.engine.Match("GET", "example.com", "http://example.com/api", nil)
	if match2 != nil {
		t.Error("engine.Match should not find rule after DELETE")
	}
}

func TestGenerateID(t *testing.T) {
	id1 := generateID()
	id2 := generateID()
	if id1 == "" || id2 == "" {
		t.Error("generateID() returned empty string")
	}
	if id1 == id2 {
		t.Error("generateID() returned same ID twice")
	}
	if len(id1) != 32 {
		t.Errorf("generateID() length = %d, want 32", len(id1))
	}
}

func TestHandleRules_GET_PersistsAcrossReload(t *testing.T) {
	s, _, _ := newTestServer(t)

	// Create two rules with a small delay to ensure distinct timestamps
	for _, name := range []string{"Rule A", "Rule B"} {
		ruleBody := rules.Rule{Name: name, Match: rules.MatchRule{Method: "GET"}, Action: rules.ActionPassthrough}
		body, _ := json.Marshal(ruleBody)
		req := httptest.NewRequest("POST", "/api/rules", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleRules(w, req)
		time.Sleep(10 * time.Millisecond)
	}

	// List
	listReq := httptest.NewRequest("GET", "/api/rules", nil)
	listW := httptest.NewRecorder()
	s.handleRules(listW, listReq)

	var result []*rules.Rule
	json.NewDecoder(listW.Body).Decode(&result)
	if len(result) != 2 {
		t.Fatalf("GET /api/rules = %d, want 2", len(result))
	}
}
