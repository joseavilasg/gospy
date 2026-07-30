package session

import (
	"os"
	"path/filepath"
	"sync"

	"gospy/internal/history"
)

type Recorder struct {
	store   *Store
	histDir string
	mu      sync.Mutex
	pending map[string]*Entry
}

func NewRecorder(histDir, sessionDir string) *Recorder {
	s, _ := NewOrLoad(sessionDir)
	return &Recorder{
		store:   s,
		histDir: histDir,
		pending: make(map[string]*Entry),
	}
}

func (r *Recorder) Subscribe(hist *history.Store) {
	hist.OnSave(r.onSave)
	hist.OnUpdate(r.onUpdate)
}

func (r *Recorder) onSave(entry *history.Entry) {
	e := &Entry{
		ID:        entry.ID,
		Timestamp: entry.Timestamp,
		Request: ReqRecord{
			Method:  entry.Request.Method,
			URL:     entry.Request.URL,
			Host:    entry.Request.Host,
			Headers: entry.Request.Headers,
		},
	}

	if body := r.readBin(entry.ID, entry.Request.BodyFile); body != nil {
		filename, _ := r.store.SaveBin(entry.ID, "req", body)
		e.Request.BodyFile = filename
		e.Request.BodySize = int64(len(body))
	}

	r.mu.Lock()
	r.pending[entry.ID] = e
	r.mu.Unlock()
}

func (r *Recorder) onUpdate(entry *history.Entry) {
	r.mu.Lock()
	e, ok := r.pending[entry.ID]
	r.mu.Unlock()
	if !ok || entry.Response == nil {
		return
	}

	e.Response = RespRecord{
		Status:  entry.Response.Status,
		Headers: entry.Response.Headers,
	}

	if body := r.readBin(entry.ID, entry.Response.BodyFile); body != nil {
		filename, _ := r.store.SaveBin(entry.ID, "resp", body)
		e.Response.BodyFile = filename
		e.Response.BodySize = int64(len(body))
	}

	r.store.SaveEntry(e)

	r.mu.Lock()
	delete(r.pending, entry.ID)
	r.mu.Unlock()
}

func (r *Recorder) readBin(entryID, bodyFile string) []byte {
	if bodyFile == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(r.histDir, "bin", bodyFile))
	if err != nil {
		return nil
	}
	return data
}
