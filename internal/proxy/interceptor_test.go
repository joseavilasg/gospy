package proxy

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

	result := decompressBody(buf.Bytes(), "gzip")
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

	result := decompressBody(buf.Bytes(), "deflate")
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

	result := decompressBody(flatBuf, "deflate")
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

	result := decompressBody(buf.Bytes(), "br")
	if result.Decoded != original {
		t.Errorf("brotli decoded: got %q, want %q", result.Decoded, original)
	}
	if result.Compression != "brotli" {
		t.Errorf("brotli compression: got %q, want %q", result.Compression, "brotli")
	}
}

func TestDecompressBody_PlainText(t *testing.T) {
	original := `{"plain":"no compression"}`
	result := decompressBody([]byte(original), "")
	if result.Decoded != original {
		t.Errorf("plain decoded: got %q, want %q", result.Decoded, original)
	}
	if result.Compression != "" {
		t.Errorf("plain compression: got %q, want empty", result.Compression)
	}
}

func TestDecompressBody_Empty(t *testing.T) {
	result := decompressBody([]byte{}, "")
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

	result := decompressBody(flatBuf, "")
	if result.Decoded == original {
		t.Error("deflate without header should not decompress")
	}
	if result.Compression != "" {
		t.Errorf("deflate without header: compression should be empty, got %q", result.Compression)
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
