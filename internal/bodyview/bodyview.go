// Package bodyview analyzes raw HTTP request/response bodies for display:
// type detection, structured parsing of protobuf and multipart payloads, and
// hex dump rendering.
//
// The functions are pure (they operate on []byte/string inputs, with no
// filesystem or network access), so they are shared by the WebUI detail view
// and the agent MCP's get_entry without duplication.
package bodyview

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gospy/internal/history"

	"google.golang.org/protobuf/encoding/protowire"
)

// GenerateHexDump renders a classic hex dump of data: an address column, up to
// 16 bytes per line with a gap after the 8th byte, and an ASCII column that
// shows printable bytes and replaces everything else with '.'. When data
// exceeds maxLines*16 bytes, trailing content is summarized with a truncation
// marker.
func GenerateHexDump(data []byte, maxLines int) string {
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

// IsProtobufContentType reports whether a Content-Type value indicates a
// protobuf payload.
func IsProtobufContentType(ct string) bool {
	lct := strings.ToLower(ct)
	return strings.Contains(lct, "protobuf") || strings.Contains(lct, "x-protobuf")
}

const protobufMaxDepth = 12

// ParseProtobufWire parses a protobuf wire-format payload into a list of
// fields for display. Nested messages are parsed recursively up to
// protobufMaxDepth.
func ParseProtobufWire(data []byte) []history.ProtobufField {
	return ParseProtobufWireAtDepth(data, 0)
}

// ParseProtobufWireAtDepth is ParseProtobufWire with an explicit recursion
// depth bound.
func ParseProtobufWireAtDepth(data []byte, depth int) []history.ProtobufField {
	if depth >= protobufMaxDepth {
		return nil
	}
	var fields []history.ProtobufField
	offset := 0
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			break
		}
		data = data[n:]

		fieldStart := offset
		offset += n

		f := history.ProtobufField{
			FieldNumber: int(num),
			ByteOffset:  fieldStart,
		}

		switch typ {
		case protowire.VarintType:
			v, n2 := protowire.ConsumeVarint(data)
			if n2 < 0 {
				return fields
			}
			f.WireType = "varint"
			f.ZigzagValue = int64((v >> 1) ^ -(v & 1))
			if v <= uint64(^uint32(0)) {
				f.Value = uint32(v)
			} else {
				f.Value = v
			}
			f.ByteEnd = offset + n2
			offset += n2
			data = data[n2:]

		case protowire.Fixed32Type:
			v, n2 := protowire.ConsumeFixed32(data)
			if n2 < 0 {
				return fields
			}
			f.WireType = "fixed32"
			f.Value = v
			f.ByteEnd = offset + n2
			offset += n2
			data = data[n2:]

		case protowire.Fixed64Type:
			v, n2 := protowire.ConsumeFixed64(data)
			if n2 < 0 {
				return fields
			}
			f.WireType = "fixed64"
			f.Value = v
			f.ByteEnd = offset + n2
			offset += n2
			data = data[n2:]

		case protowire.BytesType:
			v, n2 := protowire.ConsumeBytes(data)
			if n2 < 0 {
				return fields
			}
			f.ByteSize = len(v)
			f.ByteEnd = offset + n2
			offset += n2
			data = data[n2:]

			subFields := ParseProtobufWireAtDepth(v, depth+1)
			if len(subFields) > 0 {
				f.WireType = "message"
				f.SubFields = subFields
			} else if s, ok := IsPrintableUTF8(v); ok {
				f.WireType = "string"
				f.Value = s
			} else {
				f.WireType = "bytes"
				f.Value = fmt.Sprintf("%x", v)
			}

		default:
			n2 := protowire.ConsumeFieldValue(num, typ, data)
			if n2 < 0 {
				return fields
			}
			f.WireType = fmt.Sprintf("type(%d)", int(typ))
			f.ByteEnd = offset + n2
			offset += n2
			data = data[n2:]
		}

		fields = append(fields, f)
	}
	return fields
}

// IsPrintableUTF8 reports whether b is valid UTF-8 containing no control
// characters other than whitespace; when it is, it returns the decoded string.
func IsPrintableUTF8(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	if !utf8.Valid(b) {
		return "", false
	}
	s := string(b)
	for _, r := range s {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			return "", false
		}
	}
	return s, true
}

// ExtractBoundary returns the value of the boundary= parameter of a multipart
// Content-Type, or the empty string when absent.
func ExtractBoundary(contentType string) string {
	_, after, ok := strings.Cut(contentType, "boundary=")
	if !ok {
		return ""
	}
	b := after
	b = strings.Trim(b, "\"' ")
	return b
}

const multipartHexPreviewLines = 20

// ParseMultipartBody parses a multipart/form-data payload into its parts for
// display. Binary parts (non-text content type or containing null bytes) get a
// hex preview; text parts keep their decoded value.
func ParseMultipartBody(data []byte, boundary string) []history.MultipartPart {
	if boundary == "" || len(data) == 0 {
		return nil
	}
	reader := multipart.NewReader(bytes.NewReader(data), boundary)
	var parts []history.MultipartPart
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		name := part.FormName()
		if name == "" {
			name = part.FileName()
		}
		mp := history.MultipartPart{
			Name:        name,
			Filename:    part.FileName(),
			ContentType: part.Header.Get("Content-Type"),
		}
		content, err := io.ReadAll(part)
		if err != nil {
			parts = append(parts, mp)
			continue
		}
		mp.Size = len(content)
		if mp.Filename != "" || (!IsLikelyTextContentType(mp.ContentType) && ContainsNullBytes(content)) {
			mp.IsBinary = true
			mp.HexPreview = GenerateHexDump(content, multipartHexPreviewLines)
		} else {
			mp.Value = string(content)
		}
		parts = append(parts, mp)
	}
	return parts
}

// IsLikelyTextContentType reports whether a Content-Type value is most likely
// a human-readable text payload.
func IsLikelyTextContentType(ct string) bool {
	ct = strings.ToLower(ct)
	for _, prefix := range []string{
		"text/", "application/json", "application/xml",
		"application/x-www-form-urlencoded", "application/javascript",
		"application/css", "application/x-yaml", "application/yaml",
	} {
		if strings.HasPrefix(ct, prefix) {
			return true
		}
	}
	return false
}

// ContainsNullBytes reports whether data contains a NUL byte within its first
// 8 KiB.
func ContainsNullBytes(data []byte) bool {
	limit := 8192
	if len(data) < limit {
		limit = len(data)
	}
	for _, b := range data[:limit] {
		if b == 0 {
			return true
		}
	}
	return false
}

// DecodeBody decodes a raw body string according to its Content-Encoding header.
func DecodeBody(raw string, headers map[string][]string) string {
	enc := ""
	if ce := headers["Content-Encoding"]; len(ce) > 0 {
		enc = ce[0]
	}
	return history.DecompressBody([]byte(raw), enc).Decoded
}

// ResponseBodyForSearch returns the searchable body of a response record: the
// decoded Body when present, otherwise a best-effort decode of RawBody. Empty
// when the response is binary or has no body.
func ResponseBodyForSearch(resp *history.ResponseRecord) string {
	if resp == nil {
		return ""
	}
	if resp.Body != "" {
		return resp.Body
	}
	if resp.RawBody == "" {
		return ""
	}
	ces := resp.Headers["Content-Encoding"]
	enc := ""
	if len(ces) > 0 {
		enc = ces[0]
	}
	result := history.DecompressBody([]byte(resp.RawBody), enc)
	if len(strings.TrimSpace(result.Decoded)) > 0 {
		return result.Decoded
	}
	if result.Compression != "" {
		return ""
	}
	return resp.RawBody
}

// ReadBodyPreview reads up to limit bytes of a body file at path and appends
// the truncation marker when the file is larger.
func ReadBodyPreview(path string, limit int) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", false
	}
	if len(data) > limit {
		return string(data[:limit]) + "\n... [truncated - body too large]", true
	}
	return string(data), true
}

// ReadRawPreview reads up to limit bytes of a body file at path without a
// truncation marker. Used by the stream hub where truncation is signaled
// separately via a boolean and size.
func ReadRawPreview(path string, limit int) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// ReadBodyFile reads up to limit bytes of a body file stored under dir/bin/bodyFile.
func ReadBodyFile(dir, bodyFile string, limit int) string {
	preview, _ := ReadBodyPreview(filepath.Join(dir, "bin", bodyFile), limit)
	return preview
}
