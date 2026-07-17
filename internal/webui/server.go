package webui

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"gospy/internal/history"
)

//go:embed index.html
var indexHTML string

type Server struct {
	history *history.Store
	addr    string
}

func NewServer(addr string, h *history.Store) *Server {
	return &Server{
		history: h,
		addr:    addr,
	}
}

func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/requests", s.handleListRequests)
	mux.HandleFunc("/api/requests/", s.handleGetRequest)

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

func (s *Server) handleListRequests(w http.ResponseWriter, r *http.Request) {
	entries := s.history.List()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(entries)
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

func LogWebUI(addr string) {
	fmt.Printf("\033[36m%s\033[0m %s\n", "WEBUI", "http://"+addr)
}
