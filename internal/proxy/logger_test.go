package proxy

import (
	"testing"
)

func TestReadBodyString(t *testing.T) {
	result := ReadBodyString(nil)
	if result != "" {
		t.Errorf("ReadBodyString(nil) = %q, want empty", result)
	}
}
