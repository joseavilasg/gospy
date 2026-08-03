package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gospy/internal/history"
)

// Manager creates history-backed session directories under a base directory.
// Names are validated (no path separators, no "..") and either used verbatim or
// generated from the current timestamp when the caller passes an empty name.
type Manager struct {
	base string

	mu sync.Mutex
}

// NewManager returns a Manager that creates sessions under base.
func NewManager(base string) *Manager {
	return &Manager{base: base}
}

// Base returns the directory sessions are created under.
func (m *Manager) Base() string {
	return m.base
}

// Start creates a new session directory and history store. An empty name
// generates one from the current time (20060102-150405); collisions get a
// -2/-3 suffix. The returned store is fully functional: the session is
// replayable as soon as the first entry is saved.
func (m *Manager) Start(name string) (string, *history.Store, error) {
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, '\\') ||
		name == ".." || strings.HasPrefix(name, ".") {
		return "", nil, fmt.Errorf("invalid session name %q", name)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.base, 0o755); err != nil {
		return "", nil, err
	}

	if name == "" {
		name = time.Now().Format("20060102-150405")
	}
	dir := filepath.Join(m.base, name)
	for i := 2; ; i++ {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			break
		}
		dir = filepath.Join(m.base, fmt.Sprintf("%s-%d", name, i))
	}

	store, err := history.New(dir)
	if err != nil {
		return "", nil, err
	}
	return dir, store, nil
}
