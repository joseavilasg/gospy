package webui

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gospy/internal/history"
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
	addr        string
}

func NewServer(addr string, h *history.Store, ignore IgnoreChecker, focus FocusChecker) *Server {
	return &Server{
		history:     h,
		ignoreStore: ignore,
		focusStore:  focus,
		addr:        addr,
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
	mux.HandleFunc("/api/requests", s.handleListRequests)
	mux.HandleFunc("/api/requests/", s.handleGetRequest)
	mux.HandleFunc("/api/ignored", s.handleIgnored)
	mux.HandleFunc("/api/ignored/", s.handleIgnoredHost)
	mux.HandleFunc("/api/focused", s.handleFocused)
	mux.HandleFunc("/api/focused/", s.handleFocusedHost)

	LogWebUI(s.addr)

	return http.ListenAndServe(s.addr, mux)
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
	id := strings.TrimPrefix(r.URL.Path, "/api/requests/")
	if id == "" {
		http.NotFound(w, r)
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
