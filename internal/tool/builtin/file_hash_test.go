package builtin

import (
	"regexp"
	"testing"
)

var hexPattern = regexp.MustCompile(`^[0-9A-F]{8}$`)

func TestFileContentHash(t *testing.T) {
	tests := []struct {
		name      string
		input     []byte
		wantMatch string // if non-empty, result must equal this
	}{
		{
			name:  "empty content",
			input: []byte(""),
		},
		{
			name:  "simple content",
			input: []byte("hello\n"),
		},
		{
			name:  "different content produces different hash",
			input: []byte("bbb\n"),
		},
		{
			name:  "binary-ish content with nulls",
			input: []byte("hel\x00lo\nwor\x00ld\n"),
		},
	}

	// Collect results for cross-case assertions.
	results := make(map[string]string)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fileContentHash(tt.input)
			if !hexPattern.MatchString(got) {
				t.Fatalf("fileContentHash() = %q, want 8-char uppercase hex", got)
			}
			results[tt.name] = got
		})
	}

	// Different content must produce different hashes.
	if results["simple content"] == results["different content produces different hash"] {
		t.Errorf("expected different hashes for different content, both got %s", results["simple content"])
	}

	// Deterministic: calling twice with same input returns same output.
	t.Run("deterministic", func(t *testing.T) {
		input := []byte("determinism check\n")
		first := fileContentHash(input)
		second := fileContentHash(input)
		if first != second {
			t.Errorf("non-deterministic: first=%s second=%s", first, second)
		}
	})
}

func TestFileContentHashTrailingWhitespaceEquivalence(t *testing.T) {
	pairs := []struct {
		name string
		a    []byte
		b    []byte
	}{
		{
			name: "trailing spaces and tabs stripped",
			a:    []byte("hello  \nworld\t\n"),
			b:    []byte("hello\nworld\n"),
		},
		{
			name: "CRLF vs LF",
			a:    []byte("hello\r\nworld\r\n"),
			b:    []byte("hello\nworld\n"),
		},
		{
			name: "mixed trailing whitespace",
			a:    []byte("foo \t \r\nbar\t\t\r\n"),
			b:    []byte("foo\nbar\n"),
		},
		{
			name: "trailing spaces only on some lines",
			a:    []byte("aaa   \nbbb\nccc \n"),
			b:    []byte("aaa\nbbb\nccc\n"),
		},
	}

	for _, tt := range pairs {
		t.Run(tt.name, func(t *testing.T) {
			hashA := fileContentHash(tt.a)
			hashB := fileContentHash(tt.b)
			if hashA != hashB {
				t.Errorf("hashes differ: %q→%s vs %q→%s", tt.a, hashA, tt.b, hashB)
			}
		})
	}
}
