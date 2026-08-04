package history

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
)

// DecompressResult holds the decoded body plus the raw bytes and the detected
// compression scheme.
type DecompressResult struct {
	Decoded     string
	Raw         string
	Compression string
}

// DecompressBody decompresses raw body bytes using magic-byte detection first
// and the Content-Encoding header as a fallback, mirroring how the interceptor
// stores bodies so replay events render identically to captured entries.
func DecompressBody(data []byte, contentEncoding string) DecompressResult {
	if len(data) == 0 {
		return DecompressResult{}
	}

	raw := string(data)

	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err == nil {
			defer reader.Close()
			if decompressed, err := io.ReadAll(reader); err == nil {
				return DecompressResult{Decoded: string(decompressed), Raw: raw, Compression: "gzip"}
			}
		}
	}

	if data[0] == 0x78 {
		reader, err := zlib.NewReader(bytes.NewReader(data))
		if err == nil {
			defer reader.Close()
			if decompressed, err := io.ReadAll(reader); err == nil {
				return DecompressResult{Decoded: string(decompressed), Raw: raw, Compression: "zlib"}
			}
		}
	}

	if len(contentEncoding) > 0 && strings.Contains(strings.ToLower(contentEncoding), "br") {
		reader := brotli.NewReader(bytes.NewReader(data))
		if decompressed, err := io.ReadAll(reader); err == nil {
			return DecompressResult{Decoded: string(decompressed), Raw: raw, Compression: "brotli"}
		}
	}

	if len(contentEncoding) > 0 && strings.Contains(strings.ToLower(contentEncoding), "deflate") {
		reader := flate.NewReader(bytes.NewReader(data))
		if decompressed, err := io.ReadAll(reader); err == nil {
			return DecompressResult{Decoded: string(decompressed), Raw: raw, Compression: "deflate"}
		}
	}

	return DecompressResult{Decoded: raw}
}

// IsTextResponse reports whether a content type looks like a text format.
func IsTextResponse(contentType string) bool {
	textTypes := []string{
		"application/json",
		"application/x-www-form-urlencoded",
		"text/html",
		"text/plain",
		"text/css",
		"text/javascript",
		"application/javascript",
		"application/xml",
		"text/xml",
		"text/csv",
		"text/markdown",
	}
	ct := strings.ToLower(contentType)
	for _, t := range textTypes {
		if strings.Contains(ct, t) {
			return true
		}
	}
	return false
}

func isKnownBinaryContentType(contentType string) bool {
	ct := strings.ToLower(contentType)
	knownBinary := []string{
		"application/x-protobuf",
		"application/protobuf",
		"application/msgpack",
	}
	for _, t := range knownBinary {
		if strings.Contains(ct, t) {
			return true
		}
	}
	if strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "audio/") || strings.HasPrefix(ct, "video/") || strings.HasPrefix(ct, "font/") {
		return true
	}
	return false
}

// IsBinaryBody reports whether body bytes should be treated as binary: a known
// binary content type, or a null byte within the first 8KB. The Content-Encoding
// header marks encoded payloads as binary unless the type looks like text.
func IsBinaryBody(data []byte, contentEncoding, contentType string) bool {
	if len(data) == 0 {
		return false
	}
	if contentEncoding != "" {
		return !IsTextResponse(contentType)
	}
	if isKnownBinaryContentType(contentType) {
		return true
	}
	checkLen := min(8192, len(data))
	for i := 0; i < checkLen; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}
