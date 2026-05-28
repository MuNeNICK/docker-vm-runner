package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, false)
	logger.Print(Info, "test message")
	out := buf.String()
	if !strings.Contains(out, "[INFO]") {
		t.Fatalf("log output missing level: %q", out)
	}
	if !strings.Contains(out, "test message") {
		t.Fatalf("log output missing message: %q", out)
	}
}

func TestLoggerDebugSuppressedByDefault(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, false)
	logger.Print(Debug, "should not appear")
	if buf.String() != "" {
		t.Fatalf("debug log was not suppressed: %q", buf.String())
	}
}
