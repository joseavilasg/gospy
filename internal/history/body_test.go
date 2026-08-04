package history

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"testing"

	"github.com/andybalholm/brotli"
)

func TestDecompressBody_Gzip(t *testing.T) {
	original := `{"key":"value","method":"POST"}`
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write([]byte(original))
	w.Close()

	result := DecompressBody(buf.Bytes(), "gzip")
	if result.Decoded != original {
		t.Errorf("gzip decoded: got %q, want %q", result.Decoded, original)
	}
	if result.Compression != "gzip" {
		t.Errorf("gzip compression: got %q, want %q", result.Compression, "gzip")
	}
	if result.Raw == "" {
		t.Error("gzip: raw should not be empty")
	}
}

func TestDecompressBody_Zlib(t *testing.T) {
	original := `{"status":200,"body":"hello world"}`
	var buf bytes.Buffer
	w, _ := zlib.NewWriterLevel(&buf, zlib.DefaultCompression)
	w.Write([]byte(original))
	w.Close()

	result := DecompressBody(buf.Bytes(), "deflate")
	if result.Decoded != original {
		t.Errorf("zlib decoded: got %q, want %q", result.Decoded, original)
	}
	if result.Compression != "zlib" {
		t.Errorf("zlib compression: got %q, want %q", result.Compression, "zlib")
	}
}

func TestDecompressBody_Deflate(t *testing.T) {
	original := `{"host":"example.com","path":"/api"}`

	flatBuf, err := flatten([]byte(original))
	if err != nil {
		t.Fatalf("flate compress: %v", err)
	}

	result := DecompressBody(flatBuf, "deflate")
	if result.Decoded != original {
		t.Errorf("deflate decoded: got %q, want %q", result.Decoded, original)
	}
	if result.Compression != "deflate" {
		t.Errorf("deflate compression: got %q, want %q", result.Compression, "deflate")
	}
}

func TestDecompressBody_Brotli(t *testing.T) {
	original := `{"content":"brotli compressed data"}`
	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	w.Write([]byte(original))
	w.Close()

	result := DecompressBody(buf.Bytes(), "br")
	if result.Decoded != original {
		t.Errorf("brotli decoded: got %q, want %q", result.Decoded, original)
	}
	if result.Compression != "brotli" {
		t.Errorf("brotli compression: got %q, want %q", result.Compression, "brotli")
	}
}

func TestDecompressBody_PlainText(t *testing.T) {
	original := `{"plain":"no compression"}`
	result := DecompressBody([]byte(original), "")
	if result.Decoded != original {
		t.Errorf("plain decoded: got %q, want %q", result.Decoded, original)
	}
	if result.Compression != "" {
		t.Errorf("plain compression: got %q, want empty", result.Compression)
	}
}

func TestDecompressBody_Empty(t *testing.T) {
	result := DecompressBody([]byte{}, "")
	if result.Decoded != "" {
		t.Errorf("empty decoded: got %q, want empty string", result.Decoded)
	}
}

func TestDecompressBody_DeflateWithoutHeader(t *testing.T) {
	original := `{"host":"example.com","path":"/api"}`

	flatBuf, err := flatten([]byte(original))
	if err != nil {
		t.Fatalf("flate compress: %v", err)
	}

	result := DecompressBody(flatBuf, "")
	if result.Decoded == original {
		t.Error("deflate without header should not decompress")
	}
	if result.Compression != "" {
		t.Errorf("deflate without header: compression should be empty, got %q", result.Compression)
	}
}

func TestIsBinaryBody(t *testing.T) {
	if IsBinaryBody(nil, "", "text/plain") {
		t.Error("empty body should not be binary")
	}
	if IsBinaryBody([]byte("hello world"), "", "text/plain") {
		t.Error("plain text should not be binary")
	}
	if !IsBinaryBody([]byte{0x00, 0x01}, "", "application/octet-stream") {
		t.Error("null-byte body should be binary")
	}
	if !IsBinaryBody([]byte("data"), "", "image/png") {
		t.Error("image content type should be binary")
	}
	if IsBinaryBody([]byte{0x1f, 0x8b, 0x00}, "gzip", "application/json") {
		t.Error("gzip json should not be binary")
	}
}

func TestIsTextResponse(t *testing.T) {
	tests := []struct {
		ct     string
		expect bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"text/html", true},
		{"text/plain", true},
		{"text/css", true},
		{"text/javascript", true},
		{"application/javascript", true},
		{"application/xml", true},
		{"text/xml", true},
		{"image/png", false},
		{"application/octet-stream", false},
		{"", false},
	}

	for _, tt := range tests {
		result := IsTextResponse(tt.ct)
		if result != tt.expect {
			t.Errorf("IsTextResponse(%q) = %v, want %v", tt.ct, result, tt.expect)
		}
	}
}

func flatten(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
