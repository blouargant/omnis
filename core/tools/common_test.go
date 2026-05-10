package tools

import (
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	t.Parallel()

	short := "hello"
	if truncate(short) != short {
		t.Fatalf("truncate(short) changed the string")
	}

	long := strings.Repeat("x", MaxToolOutput+100)
	result := truncate(long)
	if len(result) <= MaxToolOutput {
		// truncated prefix + suffix message
	}
	if !strings.Contains(result, "truncated") {
		t.Fatalf("truncate(long) missing truncation marker, got len=%d", len(result))
	}
	if strings.HasPrefix(result, long[:MaxToolOutput+1]) {
		t.Fatalf("truncate(long) did not cut at MaxToolOutput")
	}
}
