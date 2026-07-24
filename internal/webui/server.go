package webui

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gospy/internal/history"
	"gospy/internal/proxy"
	"gospy/internal/rules"
)

//go:embed index.html
var indexHTML string

//go:embed style.css
var styleCSS string

//go:embed app.js
var appJS string

//go:embed state.js
var stateJS string

//go:embed api.js
var apiJS string

//go:embed render.js
var renderJS string

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
	history     *history.Store
	ignoreStore IgnoreChecker
	focusStore  FocusChecker
	rulesStore  *rules.Store
	engine      *rules.Engine
	addr        string
	resolver    ProcessResolver
	sigCache    SignatureChecker
}

type ProcessResolver interface {
	Resolve(remoteAddr string) *proxy.ProcessInfo
	GetAllProcesses() map[string]*proxy.ProcessInfo
}

type SignatureChecker interface {
	Get(filePath string) *proxy.SignatureResult
	VerifyAsync(filePath string)
	OnUpdate(fn func(*proxy.SignatureResult))
}

func NewServer(addr string, h *history.Store, ignore IgnoreChecker, focus FocusChecker, rulesStore *rules.Store, engine *rules.Engine, resolver ProcessResolver, sigCache SignatureChecker) *Server {
	return &Server{
		history:     h,
		ignoreStore: ignore,
		focusStore:  focus,
		rulesStore:  rulesStore,
		engine:      engine,
		addr:        addr,
		resolver:    resolver,
		sigCache:    sigCache,
	}
}

func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/style.css", s.handleStatic(styleCSS, "text/css"))
	mux.HandleFunc("/app.js", s.handleStatic(appJS, "application/javascript"))
	mux.HandleFunc("/state.js", s.handleStatic(stateJS, "application/javascript"))
	mux.HandleFunc("/api.js", s.handleStatic(apiJS, "application/javascript"))
	mux.HandleFunc("/render.js", s.handleStatic(renderJS, "application/javascript"))
	mux.HandleFunc("/json-viewer.js", s.handleStatic(jsonViewerJS, "application/javascript"))
	mux.HandleFunc("/json-viewer.css", s.handleStatic(jsonViewerCSS, "text/css"))
	mux.HandleFunc("/monaco-init.js", s.handleStatic(monacoInitJS, "application/javascript"))
	mux.HandleFunc("/monaco/", handleMonacoFile)
	mux.HandleFunc("/api/requests", s.handleListRequests)
	mux.HandleFunc("/api/requests/", s.handleGetRequest)
	mux.HandleFunc("/api/ignored", s.handleIgnored)
	mux.HandleFunc("/api/ignored/", s.handleIgnoredHost)
	mux.HandleFunc("/api/focused", s.handleFocused)
	mux.HandleFunc("/api/focused/", s.handleFocusedHost)
	mux.HandleFunc("/api/rules/check-match", s.handleCheckMatch)
	mux.HandleFunc("/api/rules", s.handleRules)
	mux.HandleFunc("/api/rules/", s.handleRule)
	mux.HandleFunc("/api/request-rule", s.handleRequestRule)
	mux.HandleFunc("/api/process/signature", s.handleProcessSignature)
	mux.HandleFunc("/api/process/events", s.handleProcessEvents)

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

func (s *Server) handleListRequests(w http.ResponseWriter, r *http.Request) {
	var entries []*history.ListEntry

	if since := r.URL.Query().Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339Nano, since)
		if err == nil {
			entries = s.history.ListSince(t)
		}
	}

	if entries == nil {
		entries = s.history.ListSummary()
	}

	filtered := make([]*history.ListEntry, 0, len(entries))
	for _, e := range entries {
		if s.ignoreStore.Matches(e.Host) {
			continue
		}
		filtered = append(filtered, e)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(filtered)
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
		default:
			http.NotFound(w, r)
		}
		return
	}

	entry, err := s.history.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(entry)
}

func (s *Server) handleSaveBody(w http.ResponseWriter, r *http.Request, id string) {
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

	if err := s.history.SaveEditedBody(id, body.Target, body.Body); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleRevertBody(w http.ResponseWriter, r *http.Request, id string) {
	target := r.URL.Query().Get("target")
	if target != "request" && target != "response" {
		http.Error(w, `{"error":"target must be request or response"}`, http.StatusBadRequest)
		return
	}

	if err := s.history.RevertBody(id, target); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleSaveHeaders(w http.ResponseWriter, r *http.Request, id string) {
	var payload struct {
		Headers map[string][]string `json:"headers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}

	if err := s.history.SaveEditedHeaders(id, payload.Headers); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleRevertHeaders(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.history.RevertHeaders(id); err != nil {
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

	original, err := s.history.Get(id)
	if err != nil {
		http.Error(w, `{"error":"original request not found"}`, http.StatusNotFound)
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

	if err := s.history.Save(newEntry); err != nil {
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
		json.NewEncoder(w).Encode(map[string]interface{}{"rule": rule, "deactivated": deactivated})
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
				json.NewEncoder(w).Encode(map[string]interface{}{"rule": rule, "deactivated": deactivated})
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

	entry, err := s.history.Get(id)
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
			json.NewEncoder(w).Encode(map[string]interface{}{"status": "unsupported"})
			return
		}
		result := s.sigCache.Get(filePath)
		if result == nil {
			s.sigCache.VerifyAsync(filePath)
			json.NewEncoder(w).Encode(map[string]interface{}{"status": "analyzing", "filePath": filePath})
			return
		}
		json.NewEncoder(w).Encode(result)
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
