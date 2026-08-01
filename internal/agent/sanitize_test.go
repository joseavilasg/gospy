package agent

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"gospy/internal/history"
)

var redactedRe = regexp.MustCompile(`^<redacted len=\d+>$`)

func TestSanitizeHeaders(t *testing.T) {
	in := map[string][]string{
		"Authorization":    {"Bearer secret-token"},
		"Cookie":           {"a=1", "b=2"},
		"Set-Cookie":       {"sid=1; HttpOnly"},
		"WWW-Authenticate": {`Basic realm="x"`},
		"X-Auth-Token":     {"tok123"},
		"X-CSRF-TOKEN":     {"csrf"},
		"X-API-Key-V2":     {"key"},
		"Content-Type":     {"application/json"},
		"X-Business":       {"ok"},
	}
	out := SanitizeHeaders(in)

	if in["Authorization"][0] != "Bearer secret-token" {
		t.Fatal("input map was mutated")
	}

	sensitive := []string{
		"Authorization", "Cookie", "Set-Cookie", "WWW-Authenticate",
		"X-Auth-Token", "X-CSRF-TOKEN", "X-API-Key-V2",
	}
	for _, h := range sensitive {
		for _, v := range out[h] {
			if !redactedRe.MatchString(v) {
				t.Errorf("%s value %q is not redacted", h, v)
			}
		}
	}
	if len(out["Cookie"]) != 2 {
		t.Errorf("Cookie must redact every value, got %v", out["Cookie"])
	}
	if out["Content-Type"][0] != "application/json" || out["X-Business"][0] != "ok" {
		t.Errorf("non-sensitive headers must survive, got %v %v", out["Content-Type"], out["X-Business"])
	}
	if out["Authorization"][0] != "<redacted len=19>" {
		t.Errorf("len placeholder = %q", out["Authorization"][0])
	}
}

func TestSanitizeHeaders_Heuristics(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0Z3e9W8WjL2nG4v1k"
	jwe := "eyJhbGciOiJIUzI1NiJ9.eyJwYXlsb2FkIjoie30ifQ.aGVsbG8.e30.4wUJQQr-9F0Mk3oGCvK5ZQ"
	in := map[string][]string{
		"X-Token":             {"t"},
		"X-Access-Token":      {"at"},
		"X-Session-Id":        {"s1"},
		"X-Client-Secret":     {"cs"},
		"Proxy-Authorization": {"Bearer opaquetokensecret"},
		"Set-Cookie2":         {"a=1"},
		"X-Signature":         {"sig"},
		"X-Monkey-Business":   {"ok"},
		"X-Keycloak-Realm":    {"realm"},
		"X-Author":            {"someone"},
		"X-Trace-Id":          {"trace"},
		"X-Request-Id":        {"req"},
		"Content-Encoding":    {"gzip"},
		"ETag":                {`"abc.def"`},
		"X-Custom-JWT":        {jwt},
		"X-Custom-Bearer":     {"Bearer abcdefghij123456"},
		"X-Mixed":             {jwt, "benign"},
	}
	out := SanitizeHeaders(in)

	// Token-name headers are fully redacted.
	for _, h := range []string{"X-Token", "X-Access-Token", "X-Session-Id", "X-Client-Secret",
		"Proxy-Authorization", "Set-Cookie2", "X-Signature"} {
		if !redactedRe.MatchString(out[h][0]) {
			t.Errorf("%s not redacted: %v", h, out[h])
		}
	}

	// Word-boundary matching: substring "key"/"auth"/"token" never fires.
	for _, h := range []string{"X-Monkey-Business", "X-Keycloak-Realm", "X-Author",
		"X-Trace-Id", "X-Request-Id", "Content-Encoding", "ETag"} {
		if out[h][0] != in[h][0] {
			t.Errorf("%s must survive untouched, got %v", h, out[h])
		}
	}

	// Value heuristics: JWT and opaque Bearer in name-clean headers are redacted.
	if out["X-Custom-JWT"][0] != fmt.Sprintf("<redacted len=%d>", len(jwt)) {
		t.Errorf("JWT value not redacted: %v", out["X-Custom-JWT"])
	}
	if out["X-Custom-Bearer"][0] != "<redacted len=23>" {
		t.Errorf("opaque Bearer value not redacted: %v", out["X-Custom-Bearer"])
	}

	// Per-value redaction: the JWT in a mixed header is redacted, "benign" stays.
	if out["X-Mixed"][0] != fmt.Sprintf("<redacted len=%d>", len(jwt)) || out["X-Mixed"][1] != "benign" {
		t.Errorf("per-value redaction failed: %v", out["X-Mixed"])
	}

	// A JWE (5 segments) is caught too.
	if got := SanitizeHeaders(map[string][]string{"X-Encrypted": {jwe}}); !redactedRe.MatchString(got["X-Encrypted"][0]) {
		t.Errorf("JWE value not redacted: %v", got["X-Encrypted"])
	}

	// Basic-style auth on a name-sensitive header is redacted by name alone.
	if got := SanitizeHeaders(map[string][]string{"Authorization": {"Basic dXNlcjpwYXNz"}}); got["Authorization"][0] != "<redacted len=18>" {
		t.Errorf("name-based redaction must catch Basic auth: %v", got["Authorization"])
	}
}

func TestEntryDetail_SanitizesAndEnriches(t *testing.T) {
	hist := newTestHistory(t)

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(`{"hello":"world"}`))
	zw.Close()

	binFile, err := hist.SaveBinaryBody("test-entry", "req", []byte{0x00, 0x01, 0xfe, 0xff})
	if err != nil {
		t.Fatalf("SaveBinaryBody: %v", err)
	}

	e := &history.Entry{
		ID: "test-entry",
		Request: history.RequestRecord{
			Method:  "GET",
			URL:     "http://api.com/x",
			Host:    "api.com",
			Headers: map[string][]string{"Authorization": {"Bearer tok"}},
		},
		Response: &history.ResponseRecord{
			Status:  200,
			Headers: map[string][]string{"Set-Cookie": {"s=1"}, "Content-Encoding": {"gzip"}},
			RawBody: gz.String(),
		},
		ServerRequest: &history.RequestRecord{
			Method:  "POST",
			URL:     "http://api.com/x",
			Host:    "api.com",
			Headers: map[string][]string{"X-API-Key": {"secret"}},
		},
		ServerResponse: &history.ResponseRecord{
			Status:       200,
			Headers:      map[string][]string{"Content-Type": {"application/octet-stream"}},
			BodyFile:     binFile,
			IsBinaryBody: true,
			BodySize:     4,
		},
	}

	got := EntryDetail(hist.Dir(), e)

	if got.Request.Headers["Authorization"][0] != "<redacted len=10>" {
		t.Errorf("request auth not redacted: %v", got.Request.Headers["Authorization"])
	}
	if got.ServerRequest.Headers["X-API-Key"][0] != "<redacted len=6>" {
		t.Errorf("server request X-API-Key not redacted: %v", got.ServerRequest.Headers["X-API-Key"])
	}
	if got.Response.Headers["Set-Cookie"][0] != "<redacted len=3>" {
		t.Errorf("response Set-Cookie not redacted: %v", got.Response.Headers["Set-Cookie"])
	}
	if got.Response.Body != `{"hello":"world"}` {
		t.Errorf("gzip body not decoded, got %q", got.Response.Body)
	}
	if !strings.Contains(got.ServerResponse.BodyHex, "00000000: 00 01 fe ff") {
		t.Errorf("binary body hex dump missing, got %q", got.ServerResponse.BodyHex)
	}
	if got.Response.BodyFile != "" || got.ServerResponse.BodyFile == "" {
		t.Errorf("BodyFile must survive on the binary record only")
	}
}
