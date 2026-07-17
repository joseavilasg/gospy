package proxy

import (
	"testing"
)

func TestColorMethod(t *testing.T) {
	tests := []struct {
		method string
	}{
		{"GET"},
		{"POST"},
		{"PUT"},
		{"DELETE"},
		{"PATCH"},
		{"CONNECT"},
		{"OPTIONS"},
	}

	for _, tt := range tests {
		result := colorMethod(tt.method)
		if result == "" {
			t.Errorf("colorMethod(%s) returned empty string", tt.method)
		}
	}
}

func TestColorStatus(t *testing.T) {
	tests := []struct {
		code int
	}{
		{200},
		{201},
		{301},
		{404},
		{500},
		{503},
	}

	for _, tt := range tests {
		result := colorStatus(tt.code)
		if result == "" {
			t.Errorf("colorStatus(%d) returned empty string", tt.code)
		}
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

func TestReadBodyString(t *testing.T) {
	result := ReadBodyString(nil)
	if result != "" {
		t.Errorf("ReadBodyString(nil) = %q, want empty", result)
	}
}
