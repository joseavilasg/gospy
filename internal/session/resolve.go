package session

import (
	"path/filepath"
	"strings"
)

// ResolveDir resolves a session reference to a directory. An empty value
// returns empty (the caller decides auto mode). A bare name without a path
// separator resolves under dataDir/sessions. Anything containing a separator
// is a path (absolute or relative to the working directory) and is used as-is.
func ResolveDir(value, dataDir string) string {
	if value == "" {
		return ""
	}
	if strings.ContainsAny(value, `/\`) {
		return value
	}
	return filepath.Join(dataDir, "sessions", value)
}
