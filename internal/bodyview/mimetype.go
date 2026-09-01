package bodyview

import "strings"

// ExtFromContentType returns the file extension (including dot) for a
// Content-Type value, e.g. "application/json; charset=utf-8" → ".json".
// Unknown types fall back to ".bin".
func ExtFromContentType(ct string) string {
	if ct == "" {
		return ".bin"
	}
	// Strip parameters like "; charset=utf-8" and normalize.
	if idx := strings.Index(ct, ";"); idx >= 0 {
		ct = ct[:idx]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))

	// Exact matches first (common web types).
	switch ct {
	case "application/json":
		return ".json"
	case "application/xml", "text/xml":
		return ".xml"
	case "application/javascript", "text/javascript", "application/x-javascript":
		return ".js"
	case "text/css":
		return ".css"
	case "text/html":
		return ".html"
	case "text/plain":
		return ".txt"
	case "text/csv":
		return ".csv"
	case "application/x-ndjson", "application/ndjson":
		return ".ndjson"
	case "application/x-yaml", "application/yaml", "text/yaml":
		return ".yaml"
	case "application/toml":
		return ".toml"
	case "application/protobuf", "application/x-protobuf", "application/octet-stream":
		return ".bin"
	case "image/svg+xml":
		return ".svg"
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/x-icon", "image/vnd.microsoft.icon":
		return ".ico"
	case "image/avif":
		return ".avif"
	case "image/bmp":
		return ".bmp"
	case "image/tiff":
		return ".tiff"
	case "font/woff":
		return ".woff"
	case "font/woff2":
		return ".woff2"
	case "font/ttf":
		return ".ttf"
	case "font/otf":
		return ".otf"
	case "application/font-woff":
		return ".woff"
	case "application/font-woff2":
		return ".woff2"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav":
		return ".wav"
	case "audio/webm":
		return ".weba"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/ogg":
		return ".ogv"
	case "application/zip":
		return ".zip"
	case "application/gzip", "application/x-gzip":
		return ".gz"
	case "application/pdf":
		return ".pdf"
	case "application/wasm":
		return ".wasm"
	case "text/markdown":
		return ".md"
	}

	// Suffix-based fallbacks (e.g. application/vnd.api+json → .json).
	if strings.HasSuffix(ct, "+json") {
		return ".json"
	}
	if strings.HasSuffix(ct, "+xml") {
		return ".xml"
	}

	// Prefix-based fallbacks.
	if strings.HasPrefix(ct, "text/") {
		return ".txt"
	}

	return ".bin"
}
