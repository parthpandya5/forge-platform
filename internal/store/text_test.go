package store

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundedTextPreservesValidUTF8(t *testing.T) {
	input := strings.Repeat("é", 10)
	got := boundedText(input, 9)
	if !utf8.ValidString(got) || len(got) > 9 || got != strings.Repeat("é", 4) {
		t.Fatalf("boundedText() = %q (%d bytes)", got, len(got))
	}
}

func TestBoundedTextReplacesInvalidUTF8(t *testing.T) {
	got := boundedText("ok\xffvalue", 64)
	if !utf8.ValidString(got) || strings.ContainsRune(got, '\xff') {
		t.Fatalf("boundedText() = %q", got)
	}
}
