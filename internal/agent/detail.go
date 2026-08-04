package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gospy/internal/history"
)

const hexDumpMaxLines = 20

// EntryDetail returns a copy of the entry as served to the agent: every header
// set runs through SanitizeHeaders, compressed bodies are decoded inline, and
// binary bodies get a hex dump (same rendering as the WebUI detail panel).
// dir is the history directory used to resolve .bin body files.
func EntryDetail(dir string, e *history.Entry) *history.Entry {
	c := *e
	c.Request = sanitizeRequestRecord(dir, e.Request)
	if e.Response != nil {
		rc := sanitizeResponseRecord(dir, *e.Response)
		c.Response = &rc
	}
	if e.ServerRequest != nil {
		sr := sanitizeRequestRecord(dir, *e.ServerRequest)
		c.ServerRequest = &sr
	}
	if e.ServerResponse != nil {
		src := sanitizeResponseRecord(dir, *e.ServerResponse)
		c.ServerResponse = &src
	}
	return &c
}

func sanitizeRequestRecord(dir string, rec history.RequestRecord) history.RequestRecord {
	rec.Headers = SanitizeHeaders(rec.Headers)
	if len(rec.EditedHeaders) > 0 {
		rec.EditedHeaders = SanitizeHeaders(rec.EditedHeaders)
	}
	if rec.RawBody != "" && rec.Body == "" {
		rec.Body = decodeBody(rec.RawBody, rec.Headers)
	}
	if rec.BodyFile != "" && rec.IsBinaryBody {
		rec.BodyHex = readHexDump(dir, rec.BodyFile)
	}
	return rec
}

func sanitizeResponseRecord(dir string, rec history.ResponseRecord) history.ResponseRecord {
	rec.Headers = SanitizeHeaders(rec.Headers)
	if rec.RawBody != "" && rec.Body == "" {
		rec.Body = decodeBody(rec.RawBody, rec.Headers)
	}
	if rec.BodyFile != "" && rec.IsBinaryBody {
		rec.BodyHex = readHexDump(dir, rec.BodyFile)
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
