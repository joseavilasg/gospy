package bodyview

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gospy/internal/history"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestParseProtobufWire(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantLen int
		check   func(t *testing.T, fields []history.ProtobufField)
	}{
		{
			name:    "empty",
			data:    []byte{},
			wantLen: 0,
		},
		{
			name: "varint field",
			data: func() []byte {
				var buf []byte
				buf = protowire.AppendTag(buf, 1, protowire.VarintType)
				buf = protowire.AppendVarint(buf, 42)
				return buf
			}(),
			wantLen: 1,
			check: func(t *testing.T, fields []history.ProtobufField) {
				if fields[0].FieldNumber != 1 {
					t.Errorf("field number = %d, want 1", fields[0].FieldNumber)
				}
				if fields[0].WireType != "varint" {
					t.Errorf("wire type = %q, want varint", fields[0].WireType)
				}
				if fields[0].Value.(uint32) != 42 {
					t.Errorf("value = %v, want 42", fields[0].Value)
				}
				if fields[0].ByteOffset != 0 {
					t.Errorf("byte offset = %d, want 0", fields[0].ByteOffset)
				}
				if fields[0].ByteEnd != 2 {
					t.Errorf("byte end = %d, want 2", fields[0].ByteEnd)
				}
				if fields[0].ZigzagValue != int64(21) {
					t.Errorf("zigzag value = %v, want 21", fields[0].ZigzagValue)
				}
			},
		},
		{
			name: "zigzag signed varint",
			data: func() []byte {
				var buf []byte
				buf = protowire.AppendTag(buf, 1, protowire.VarintType)
				buf = protowire.AppendVarint(buf, 1)
				return buf
			}(),
			wantLen: 1,
			check: func(t *testing.T, fields []history.ProtobufField) {
				if fields[0].WireType != "varint" {
					t.Errorf("wire type = %q, want varint", fields[0].WireType)
				}
				if fields[0].Value.(uint32) != 1 {
					t.Errorf("value = %v, want 1", fields[0].Value)
				}
				if fields[0].ZigzagValue != int64(-1) {
					t.Errorf("zigzag value = %v, want -1", fields[0].ZigzagValue)
				}
			},
		},
		{
			name: "bytes field with hex fallback",
			data: func() []byte {
				var buf []byte
				buf = protowire.AppendTag(buf, 2, protowire.BytesType)
				buf = protowire.AppendBytes(buf, []byte{0xfe, 0xff, 0xfe, 0xff, 0xfe})
				return buf
			}(),
			wantLen: 1,
			check: func(t *testing.T, fields []history.ProtobufField) {
				if fields[0].FieldNumber != 2 {
					t.Errorf("field number = %d, want 2", fields[0].FieldNumber)
				}
				if fields[0].WireType != "bytes" {
					t.Errorf("wire type = %q, want bytes", fields[0].WireType)
				}
				if fields[0].ByteSize != 5 {
					t.Errorf("byte size = %d, want 5", fields[0].ByteSize)
				}
				if fields[0].ByteOffset != 0 {
					t.Errorf("byte offset = %d, want 0", fields[0].ByteOffset)
				}
				if fields[0].ByteEnd != 7 {
					t.Errorf("byte end = %d, want 7", fields[0].ByteEnd)
				}
				if len(fields[0].SubFields) > 0 {
					t.Errorf("expected no sub-fields for non-proto bytes, got %d", len(fields[0].SubFields))
				}
				if s, ok := fields[0].Value.(string); !ok || s != "fefffefffe" {
					t.Errorf("value = %v, want 'fefffefffe'", fields[0].Value)
				}
			},
		},
		{
			name: "nested message",
			data: func() []byte {
				var inner []byte
				inner = protowire.AppendTag(inner, 1, protowire.VarintType)
				inner = protowire.AppendVarint(inner, 99)
				var outer []byte
				outer = protowire.AppendTag(outer, 3, protowire.BytesType)
				outer = protowire.AppendBytes(outer, inner)
				return outer
			}(),
			wantLen: 1,
			check: func(t *testing.T, fields []history.ProtobufField) {
				if fields[0].WireType != "message" {
					t.Errorf("wire type = %q, want message", fields[0].WireType)
				}
				if fields[0].ByteSize != 2 {
					t.Errorf("byte size = %d, want 2", fields[0].ByteSize)
				}
				if fields[0].ByteOffset != 0 {
					t.Errorf("byte offset = %d, want 0", fields[0].ByteOffset)
				}
				if fields[0].ByteEnd != 4 {
					t.Errorf("byte end = %d, want 4", fields[0].ByteEnd)
				}
				if len(fields[0].SubFields) != 1 {
					t.Fatalf("sub fields = %d, want 1", len(fields[0].SubFields))
				}
				if fields[0].SubFields[0].FieldNumber != 1 {
					t.Errorf("sub field number = %d, want 1", fields[0].SubFields[0].FieldNumber)
				}
				if fields[0].SubFields[0].Value.(uint32) != 99 {
					t.Errorf("sub field value = %v, want 99", fields[0].SubFields[0].Value)
				}
				if fields[0].SubFields[0].ByteOffset != 0 {
					t.Errorf("sub field byte offset = %d, want 0", fields[0].SubFields[0].ByteOffset)
				}
				if fields[0].SubFields[0].ByteEnd != 2 {
					t.Errorf("sub field byte end = %d, want 2", fields[0].SubFields[0].ByteEnd)
				}
			},
		},
		{
			name: "multiple fields",
			data: func() []byte {
				var buf []byte
				buf = protowire.AppendTag(buf, 1, protowire.VarintType)
				buf = protowire.AppendVarint(buf, 100)
				buf = protowire.AppendTag(buf, 2, protowire.BytesType)
				buf = protowire.AppendString(buf, "test")
				buf = protowire.AppendTag(buf, 5, protowire.Fixed32Type)
				buf = protowire.AppendFixed32(buf, 12345)
				return buf
			}(),
			wantLen: 3,
			check: func(t *testing.T, fields []history.ProtobufField) {
				if fields[0].FieldNumber != 1 || fields[1].FieldNumber != 2 || fields[2].FieldNumber != 5 {
					t.Errorf("field numbers = %d,%d,%d, want 1,2,5",
						fields[0].FieldNumber, fields[1].FieldNumber, fields[2].FieldNumber)
				}
				if fields[1].WireType != "string" {
					t.Errorf("field 2 wire type = %q, want string", fields[1].WireType)
				}
				if fields[1].Value != "test" {
					t.Errorf("field 2 value = %v, want 'test'", fields[1].Value)
				}
				if fields[1].ByteSize != 4 {
					t.Errorf("field 2 byte size = %d, want 4", fields[1].ByteSize)
				}
				if fields[2].WireType != "fixed32" {
					t.Errorf("field 5 wire type = %q, want fixed32", fields[2].WireType)
				}
				if fields[0].ByteOffset != 0 || fields[0].ByteEnd != 2 {
					t.Errorf("field 1 range = %d-%d, want 0-2", fields[0].ByteOffset, fields[0].ByteEnd)
				}
				if fields[1].ByteOffset != 2 || fields[1].ByteEnd != 8 {
					t.Errorf("field 2 range = %d-%d, want 2-8", fields[1].ByteOffset, fields[1].ByteEnd)
				}
				if fields[2].ByteOffset != 8 || fields[2].ByteEnd != 13 {
					t.Errorf("field 5 range = %d-%d, want 8-13", fields[2].ByteOffset, fields[2].ByteEnd)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := ParseProtobufWire(tt.data)
			if len(fields) != tt.wantLen {
				t.Fatalf("len(fields) = %d, want %d", len(fields), tt.wantLen)
			}
			if tt.check != nil {
				tt.check(t, fields)
			}
		})
	}
}

func TestGenerateHexDump(t *testing.T) {
	if got := GenerateHexDump(nil, 20); got != "" {
		t.Errorf("empty input = %q, want empty", got)
	}

	out := GenerateHexDump([]byte{0x41, 0x42, 0x43}, 20)
	if !strings.Contains(out, "00000000:") {
		t.Errorf("hex dump missing address column: %q", out)
	}
	if !strings.Contains(out, "41 42 43") {
		t.Errorf("hex dump missing byte values: %q", out)
	}
	if !strings.Contains(out, "ABC") {
		t.Errorf("hex dump missing ascii column: %q", out)
	}
}

func TestGenerateHexDumpTruncation(t *testing.T) {
	data := make([]byte, 512)
	out := GenerateHexDump(data, 1)
	if !strings.Contains(out, "... (496 more bytes)") {
		t.Errorf("expected truncation marker, got %q", out)
	}
}

func TestGenerateHexDumpGapAfterEighthByte(t *testing.T) {
	data := make([]byte, 16)
	for i := range data {
		data[i] = byte(i)
	}
	out := GenerateHexDump(data, 2)
	// After the 8th byte (0x07) a double space separates the two 8-byte
	// halves of the line, so the 8th and 9th columns are not adjacent.
	if !strings.Contains(out, "07  08") {
		t.Errorf("expected a gap between the 8th and 9th byte columns, got %q", out)
	}
}

func TestIsPrintableUTF8(t *testing.T) {
	if s, ok := IsPrintableUTF8([]byte("hello")); !ok || s != "hello" {
		t.Errorf("plain text = %q, %v, want ok", s, ok)
	}
	if _, ok := IsPrintableUTF8([]byte{0xff, 0xfe}); ok {
		t.Error("invalid UTF-8 should be rejected")
	}
	if _, ok := IsPrintableUTF8([]byte{0x01}); ok {
		t.Error("control character should be rejected")
	}
	if _, ok := IsPrintableUTF8(nil); ok {
		t.Error("empty input should be rejected")
	}
}

func TestIsProtobufContentType(t *testing.T) {
	for _, ct := range []string{"application/x-protobuf", "application/protobuf", "APPLICATION/PROTOBUF"} {
		if !IsProtobufContentType(ct) {
			t.Errorf("%q should be detected as protobuf", ct)
		}
	}
	if IsProtobufContentType("application/json") {
		t.Error("json should not be detected as protobuf")
	}
}

func TestIsLikelyTextContentType(t *testing.T) {
	for _, ct := range []string{"text/html", "application/json", "text/plain"} {
		if !IsLikelyTextContentType(ct) {
			t.Errorf("%q should be likely text", ct)
		}
	}
	if IsLikelyTextContentType("application/octet-stream") {
		t.Error("octet-stream should not be likely text")
	}
}

func TestContainsNullBytes(t *testing.T) {
	if ContainsNullBytes([]byte("no null here")) {
		t.Error("plain text should not contain null bytes")
	}
	if !ContainsNullBytes([]byte{0x00}) {
		t.Error("single null byte should be detected")
	}
	if !ContainsNullBytes([]byte{1, 2, 0, 3}) {
		t.Error("null byte within data should be detected")
	}
}

func TestExtractBoundary(t *testing.T) {
	if got := ExtractBoundary("multipart/form-data; boundary=abc123"); got != "abc123" {
		t.Errorf("boundary = %q, want abc123", got)
	}
	if got := ExtractBoundary(`multipart/form-data; boundary="quoted"`); got != "quoted" {
		t.Errorf("quoted boundary = %q, want quoted", got)
	}
	if got := ExtractBoundary("application/json"); got != "" {
		t.Errorf("no boundary should be empty, got %q", got)
	}
}

func TestParseMultipartBody(t *testing.T) {
	body := "--BOUND\r\n" +
		"Content-Disposition: form-data; name=\"field\"\r\n\r\n" +
		"value-one\r\n" +
		"--BOUND\r\n" +
		"Content-Disposition: form-data; name=\"file\"; filename=\"a.bin\"\r\n" +
		"Content-Type: application/octet-stream\r\n\r\n" +
		"\x00\x01\x02\r\n" +
		"--BOUND--\r\n"
	parts := ParseMultipartBody([]byte(body), "BOUND")
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(parts))
	}
	if parts[0].Name != "field" || parts[0].Value != "value-one" {
		t.Errorf("text part = %+v, want name=field value=value-one", parts[0])
	}
	if parts[1].Name != "file" || parts[1].Filename != "a.bin" || !parts[1].IsBinary {
		t.Errorf("binary part = %+v, want file a.bin marked binary", parts[1])
	}
	if parts[1].HexPreview == "" {
		t.Error("binary part should carry a hex preview")
	}
	if ParseMultipartBody(nil, "BOUND") != nil {
		t.Error("empty body should return nil")
	}
	if ParseMultipartBody([]byte(body), "") != nil {
		t.Error("empty boundary should return nil")
	}
}

func TestDecodeBody(t *testing.T) {
	if got := DecodeBody("hello", map[string][]string{"Content-Encoding": {"gzip"}}); got == "" {
		t.Error("DecodeBody should handle plain text")
	}
	if got := DecodeBody("plain", nil); got != "plain" {
		t.Errorf("DecodeBody plain = %q, want plain", got)
	}
}

func TestResponseBodyForSearch(t *testing.T) {
	if got := ResponseBodyForSearch(nil); got != "" {
		t.Errorf("nil response = %q, want empty", got)
	}
	resp := &history.ResponseRecord{Body: "decoded", RawBody: "raw"}
	if got := ResponseBodyForSearch(resp); got != "decoded" {
		t.Errorf("Body precedence = %q, want decoded", got)
	}
	resp2 := &history.ResponseRecord{RawBody: "raw"}
	if got := ResponseBodyForSearch(resp2); got != "raw" {
		t.Errorf("RawBody fallback = %q, want raw", got)
	}
}

func TestReadBodyPreview(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/body.bin"
	content := "hello world"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, ok := ReadBodyPreview(path, 100); !ok || got != content {
		t.Errorf("ReadBodyPreview = %q, %v, want %q", got, ok, content)
	}
	if got, ok := ReadBodyPreview(path, 5); !ok || got != "hello\n... [truncated - body too large]" {
		t.Errorf("truncated preview = %q", got)
	}
	if _, ok := ReadBodyPreview(dir+"/missing", 10); ok {
		t.Error("missing file should return false")
	}
}

func TestReadRawPreview(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "raw.bin")
	content := "0123456789ABCDEF"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, ok := ReadRawPreview(path, 10); !ok || got != "0123456789" {
		t.Errorf("ReadRawPreview = %q, want 0123456789", got)
	}
	if got, ok := ReadRawPreview(path, 100); !ok || got != content {
		t.Errorf("ReadRawPreview full = %q, want %q", got, content)
	}
}

func TestReadBodyFile(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "file body content"
	if err := os.WriteFile(filepath.Join(binDir, "a.bin"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := ReadBodyFile(dir, "a.bin", 100); got != content {
		t.Errorf("ReadBodyFile = %q, want %q", got, content)
	}
	if got := ReadBodyFile(dir, "a.bin", 5); got != "file \n... [truncated - body too large]" {
		t.Errorf("ReadBodyFile truncated = %q", got)
	}
	if got := ReadBodyFile(dir, "missing.bin", 100); got != "" {
		t.Errorf("missing file should return empty, got %q", got)
	}
}

func TestDecodeBodyGzip(t *testing.T) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte("gzip decoded")); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	w.Close()
	raw := buf.String()
	got := DecodeBody(raw, map[string][]string{"Content-Encoding": {"gzip"}})
	if got != "gzip decoded" {
		t.Errorf("DecodeBody gzip = %q, want gzip decoded", got)
	}
}

func TestResponseBodyForSearchGzip(t *testing.T) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte("search me gzip")); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	w.Close()
	resp := &history.ResponseRecord{
		RawBody: buf.String(),
		Headers: map[string][]string{"Content-Encoding": {"gzip"}},
	}
	if got := ResponseBodyForSearch(resp); got != "search me gzip" {
		t.Errorf("ResponseBodyForSearch gzip = %q, want search me gzip", got)
	}
}

func TestPopulateEntry_Decompress(t *testing.T) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write([]byte("gzipped request"))
	w.Close()

	entry := &history.Entry{
		Request: history.RequestRecord{
			RawBody: buf.String(),
			Headers: map[string][]string{"Content-Encoding": {"gzip"}},
		},
	}
	PopulateEntry(entry, t.TempDir(), 64*1024)
	if entry.Request.Body != "gzipped request" {
		t.Errorf("request body = %q, want gzipped request", entry.Request.Body)
	}

	var rbuf bytes.Buffer
	rw := gzip.NewWriter(&rbuf)
	rw.Write([]byte("gzipped response"))
	rw.Close()
	entry2 := &history.Entry{
		Request: history.RequestRecord{},
		Response: &history.ResponseRecord{
			RawBody: rbuf.String(),
			Headers: map[string][]string{"Content-Encoding": {"gzip"}},
		},
	}
	PopulateEntry(entry2, t.TempDir(), 64*1024)
	if entry2.Response.Body != "gzipped response" {
		t.Errorf("response body = %q, want gzipped response", entry2.Response.Body)
	}

	entry3 := &history.Entry{
		Request: history.RequestRecord{},
		ServerResponse: &history.ResponseRecord{
			RawBody: "plain server",
		},
	}
	PopulateEntry(entry3, t.TempDir(), 64*1024)
	if entry3.ServerResponse.Body != "plain server" {
		t.Errorf("server response body = %q, want plain server", entry3.ServerResponse.Body)
	}
}

func TestPopulateEntry_Truncation(t *testing.T) {
	long := strings.Repeat("x", 1000)
	entry := &history.Entry{
		Request:        history.RequestRecord{Body: long},
		Response:       &history.ResponseRecord{Body: long},
		ServerResponse: &history.ResponseRecord{Body: long},
	}
	PopulateEntry(entry, t.TempDir(), 100)

	marker := "\n... [truncated - body too large]"
	if len(entry.Request.Body) != 100+len(marker) {
		t.Errorf("request body len = %d, want %d", len(entry.Request.Body), 100+len(marker))
	}
	if entry.Request.Body[100:] != marker {
		t.Errorf("request truncation marker = %q", entry.Request.Body[100:])
	}
	if len(entry.Response.Body) != 100+len(marker) {
		t.Errorf("response body len = %d, want %d", len(entry.Response.Body), 100+len(marker))
	}
	if len(entry.ServerResponse.Body) != 100+len(marker) {
		t.Errorf("server response body len = %d, want %d", len(entry.ServerResponse.Body), 100+len(marker))
	}
}

func TestPopulateEntry_Multipart(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0o755)

	body := "--BOUND\r\n" +
		"Content-Disposition: form-data; name=\"text\"\r\n\r\n" +
		"hello\r\n" +
		"--BOUND--\r\n"
	os.WriteFile(filepath.Join(binDir, "req.bin"), []byte(body), 0o644)

	entry := &history.Entry{
		Request: history.RequestRecord{
			BodyFile: "req.bin",
			Headers:  map[string][]string{"Content-Type": {"multipart/form-data; boundary=BOUND"}},
		},
	}
	PopulateEntry(entry, binDir, 64*1024)

	if len(entry.Request.ParsedMultipart) != 1 {
		t.Fatalf("parsed multipart = %d parts, want 1", len(entry.Request.ParsedMultipart))
	}
	if entry.Request.ParsedMultipart[0].Name != "text" {
		t.Errorf("part name = %q, want text", entry.Request.ParsedMultipart[0].Name)
	}
	if entry.Request.ParsedMultipart[0].Value != "hello" {
		t.Errorf("part value = %q, want hello", entry.Request.ParsedMultipart[0].Value)
	}
}

func TestPopulateEntry_Protobuf(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0o755)

	var pbuf []byte
	pbuf = protowire.AppendTag(pbuf, 1, protowire.VarintType)
	pbuf = protowire.AppendVarint(pbuf, 42)
	os.WriteFile(filepath.Join(binDir, "req.pb"), pbuf, 0o644)
	os.WriteFile(filepath.Join(binDir, "resp.pb"), pbuf, 0o644)

	entry := &history.Entry{
		Request: history.RequestRecord{
			BodyFile: "req.pb",
			Headers:  map[string][]string{"Content-Type": {"application/x-protobuf"}},
		},
		Response: &history.ResponseRecord{
			BodyFile: "resp.pb",
			Headers:  map[string][]string{"Content-Type": {"application/x-protobuf"}},
		},
	}
	PopulateEntry(entry, binDir, 64*1024)

	if len(entry.Request.ParsedProtobuf) != 1 {
		t.Fatalf("request protobuf fields = %d, want 1", len(entry.Request.ParsedProtobuf))
	}
	if entry.Request.ParsedProtobuf[0].Value.(uint32) != 42 {
		t.Errorf("request protobuf value = %v, want 42", entry.Request.ParsedProtobuf[0].Value)
	}
	if len(entry.Response.ParsedProtobuf) != 1 {
		t.Fatalf("response protobuf fields = %d, want 1", len(entry.Response.ParsedProtobuf))
	}
}

func TestPopulateEntry_BinaryHex(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0o755)

	binData := []byte{0xde, 0xad, 0xbe, 0xef}
	os.WriteFile(filepath.Join(binDir, "bin.req"), binData, 0o644)
	os.WriteFile(filepath.Join(binDir, "bin.resp"), binData, 0o644)
	os.WriteFile(filepath.Join(binDir, "bin.srv"), binData, 0o644)

	entry := &history.Entry{
		Request: history.RequestRecord{
			BodyFile:     "bin.req",
			IsBinaryBody: true,
		},
		Response: &history.ResponseRecord{
			BodyFile:     "bin.resp",
			IsBinaryBody: true,
		},
		ServerResponse: &history.ResponseRecord{
			BodyFile:     "bin.srv",
			IsBinaryBody: true,
		},
	}
	PopulateEntry(entry, binDir, 64*1024)

	if entry.Request.BodyHex == "" {
		t.Error("request BodyHex should not be empty")
	}
	if entry.Response.BodyHex == "" {
		t.Error("response BodyHex should not be empty")
	}
	if entry.ServerResponse.BodyHex == "" {
		t.Error("server response BodyHex should not be empty")
	}
	if !strings.Contains(entry.Request.BodyHex, "de ad be ef") {
		t.Errorf("request hex should contain 'de ad be ef', got %q", entry.Request.BodyHex)
	}
}

func TestPopulateEntry_ServerResponseBinary(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0o755)

	os.WriteFile(filepath.Join(binDir, "srv.bin"), []byte{0xca, 0xfe}, 0o644)

	entry := &history.Entry{
		Request: history.RequestRecord{},
		ServerResponse: &history.ResponseRecord{
			BodyFile:     "srv.bin",
			IsBinaryBody: true,
		},
	}
	PopulateEntry(entry, binDir, 64*1024)

	if entry.ServerResponse.BodyHex == "" {
		t.Error("server response BodyHex should not be empty for binary body")
	}
}

func TestPopulateEntry_ResponsePreview(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0o755)

	os.WriteFile(filepath.Join(binDir, "resp.txt"), []byte("file content here"), 0o644)

	entry := &history.Entry{
		Request: history.RequestRecord{},
		Response: &history.ResponseRecord{
			BodyFile:     "resp.txt",
			IsBinaryBody: false,
		},
	}
	PopulateEntry(entry, binDir, 64*1024)

	if entry.Response.Body != "file content here" {
		t.Errorf("response body from file = %q, want file content here", entry.Response.Body)
	}
	if entry.Response.BodyHex != "" {
		t.Error("non-binary response should not have BodyHex")
	}
}

func TestPopulateEntry_EmptyBodyFile(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0o755)

	entry := &history.Entry{
		Request: history.RequestRecord{
			BodyFile: "nonexistent.bin",
			Headers:  map[string][]string{"Content-Type": {"application/json"}},
		},
	}
	PopulateEntry(entry, binDir, 64*1024)

	if entry.Request.BodyHex != "" {
		t.Error("missing file should not produce hex")
	}
	if entry.Request.ParsedMultipart != nil {
		t.Error("missing file should not produce multipart")
	}
}

func TestPopulateEntry_NoBodyFile(t *testing.T) {
	entry := &history.Entry{
		Request: history.RequestRecord{Body: "inline"},
	}
	PopulateEntry(entry, t.TempDir(), 64*1024)

	if entry.Request.Body != "inline" {
		t.Errorf("request body = %q, want inline", entry.Request.Body)
	}
	if entry.Request.BodyHex != "" {
		t.Error("no BodyFile should produce no hex")
	}
}
