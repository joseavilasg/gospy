package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gospy/internal/history"
)

const (
	hexDumpMaxLines = 20
	// maxBodyPreview caps the body text loaded from a file for the agent MCP.
	// 64 KB keeps context usage reasonable while covering most JSON/HTML
	// responses; larger bodies get a truncation marker.
	maxBodyPreview = 64 * 1024
)

// EntryDetail returns a copy of the entry as served to the agent: every header
// set runs through SanitizeHeaders (unless replayMode is true, where raw
// headers are needed for debugging), compressed bodies are decoded inline, and
// binary bodies get a hex dump (same rendering as the WebUI detail panel).
// dir is the history directory used to resolve .bin body files.
func EntryDetail(dir string, e *history.Entry, replayMode ...bool) *history.Entry {
	sanitize := len(replayMode) == 0 || !replayMode[0]
	c := *e
	c.Request = sanitizeRequestRecord(dir, e.Request, sanitize)
	if e.Response != nil {
		rc := sanitizeResponseRecord(dir, *e.Response, sanitize)
		c.Response = &rc
	}
	if e.ServerRequest != nil {
		sr := sanitizeRequestRecord(dir, *e.ServerRequest, sanitize)
		c.ServerRequest = &sr
	}
	if e.ServerResponse != nil {
		src := sanitizeResponseRecord(dir, *e.ServerResponse, sanitize)
		c.ServerResponse = &src
	}
	return &c
}

func sanitizeRequestRecord(dir string, rec history.RequestRecord, sanitize bool) history.RequestRecord {
	if sanitize {
		rec.Headers = SanitizeHeaders(rec.Headers)
		if len(rec.EditedHeaders) > 0 {
			rec.EditedHeaders = SanitizeHeaders(rec.EditedHeaders)
		}
	}
	if rec.RawBody != "" && rec.Body == "" {
		rec.Body = decodeBody(rec.RawBody, rec.Headers)
	}
	if rec.BodyFile != "" && rec.IsBinaryBody {
		rec.BodyHex = readHexDump(dir, rec.BodyFile)
	} else if rec.BodyFile != "" && rec.Body == "" {
		rec.Body = readBodyFile(dir, rec.BodyFile)
	}
	return rec
}

func sanitizeResponseRecord(dir string, rec history.ResponseRecord, sanitize bool) history.ResponseRecord {
	if sanitize {
		rec.Headers = SanitizeHeaders(rec.Headers)
	}
	if rec.RawBody != "" && rec.Body == "" {
		rec.Body = decodeBody(rec.RawBody, rec.Headers)
	}
	if rec.BodyFile != "" && rec.IsBinaryBody {
		rec.BodyHex = readHexDump(dir, rec.BodyFile)
	} else if rec.BodyFile != "" && rec.Body == "" {
		rec.Body = readBodyFile(dir, rec.BodyFile)
	}
	return rec
}

func decodeBody(raw string, headers map[string][]string) string {
	enc := ""
	if ce := headers["Content-Encoding"]; len(ce) > 0 {
		enc = ce[0]
	}
	return history.DecompressBody([]byte(raw), enc).Decoded
}

func readHexDump(dir, bodyFile string) string {
	data, err := os.ReadFile(filepath.Join(dir, "bin", bodyFile))
	if err != nil {
		return ""
	}
	return generateHexDump(data, hexDumpMaxLines)
}

// readBodyFile reads up to maxBodyPreview bytes from a body file stored in
// the bin/ directory. Returns a truncated string with a marker when the file
// exceeds the limit.
func readBodyFile(dir, bodyFile string) string {
	data, err := os.ReadFile(filepath.Join(dir, "bin", bodyFile))
	if err != nil {
		return ""
	}
	if len(data) > maxBodyPreview {
		return string(data[:maxBodyPreview]) + "\n... [truncated - body too large]"
	}
	return string(data)
}

// generateHexDump mirrors the WebUI's hex dump rendering.
func generateHexDump(data []byte, maxLines int) string {
	if len(data) == 0 {
		return ""
	}
	maxBytes := maxLines * 16
	if len(data) < maxBytes {
		maxBytes = len(data)
	}
	var sb strings.Builder
	for i := 0; i < maxBytes; i += 16 {
		end := i + 16
		if end > maxBytes {
			end = maxBytes
		}
		chunk := data[i:end]
		fmt.Fprintf(&sb, "%08x: ", i)
		hex := make([]string, 0, 16)
		ascii := make([]byte, 0, 16)
		for j, b := range chunk {
			hex = append(hex, fmt.Sprintf("%02x", b))
			if j == 7 {
				hex = append(hex, "")
			}
			if b >= 32 && b <= 126 {
				ascii = append(ascii, b)
			} else {
				ascii = append(ascii, '.')
			}
		}
		fmt.Fprintf(&sb, "%-49s  %s\n", strings.Join(hex, " "), string(ascii))
	}
	if len(data) > maxBytes {
		fmt.Fprintf(&sb, "... (%d more bytes)\n", len(data)-maxBytes)
	}
	return sb.String()
}
