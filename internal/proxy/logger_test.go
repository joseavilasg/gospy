package proxy

import (
	"testing"
)

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

func TestReadBodyString(t *testing.T) {
	result := ReadBodyString(nil)
	if result != "" {
		t.Errorf("ReadBodyString(nil) = %q, want empty", result)
	}
}
