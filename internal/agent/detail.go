package agent

import (
	"os"
	"path/filepath"

	"gospy/internal/bodyview"
	"gospy/internal/history"
)

const (
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
		rec.Body = bodyview.DecodeBody(rec.RawBody, rec.Headers)
	}
	if rec.BodyFile != "" && rec.IsBinaryBody {
		rec.BodyHex = readHexDump(dir, rec.BodyFile)
	} else if rec.BodyFile != "" && rec.Body == "" {
		rec.Body = bodyview.ReadBodyFile(dir, rec.BodyFile, maxBodyPreview)
	}
	return rec
}

func sanitizeResponseRecord(dir string, rec history.ResponseRecord, sanitize bool) history.ResponseRecord {
	if sanitize {
		rec.Headers = SanitizeHeaders(rec.Headers)
	}
	if rec.RawBody != "" && rec.Body == "" {
		rec.Body = bodyview.DecodeBody(rec.RawBody, rec.Headers)
	}
	if rec.BodyFile != "" && rec.IsBinaryBody {
		rec.BodyHex = readHexDump(dir, rec.BodyFile)
	} else if rec.BodyFile != "" && rec.Body == "" {
		rec.Body = bodyview.ReadBodyFile(dir, rec.BodyFile, maxBodyPreview)
	}
	return rec
}

func readHexDump(dir, bodyFile string) string {
	data, err := os.ReadFile(filepath.Join(dir, "bin", bodyFile))
	if err != nil {
		return ""
	}
	return bodyview.GenerateHexDump(data, bodyview.HexDumpMaxLines)
}
