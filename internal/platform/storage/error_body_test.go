package storage

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestErrorBody_BoundsAndKeepsUTF8Valid(t *testing.T) {
	if got := errorBody([]byte("short")); got != "short" {
		t.Errorf("short body: got %q", got)
	}
	long := []byte(strings.Repeat("a", 511) + "é" + strings.Repeat("b", 2000))
	got := errorBody(long)
	if len(got) > 512+len("…") {
		t.Errorf("long body: %d bytes, want at most %d", len(got), 512+len("…"))
	}
	if !utf8.ValidString(got) {
		t.Errorf("long body cut through a character: %q", got[500:])
	}
}
