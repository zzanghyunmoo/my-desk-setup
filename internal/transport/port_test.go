package transport

import (
	"strings"
	"testing"
)

func TestLimitedBufferTruncatesWithoutShortWrite(t *testing.T) {
	buffer := newLimitedBuffer(4)
	input := []byte("abcdefgh")
	written, err := buffer.Write(input)
	if err != nil {
		t.Fatalf("Write(): %v", err)
	}
	if written != len(input) {
		t.Fatalf("Write() = %d, want %d", written, len(input))
	}
	if got := buffer.String(); !strings.Contains(got, "abcd") ||
		!strings.Contains(got, "[output truncated]") {
		t.Fatalf("String() = %q, want prefix and truncation marker", got)
	}
}
