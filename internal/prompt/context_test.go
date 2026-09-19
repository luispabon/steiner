package prompt

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"
)

func TestReadFileBlockTruncatesOnRuneBoundary(t *testing.T) {
	tests := []struct {
		name    string
		content string
		limit   int
		want    string
	}{
		{"two-byte rune split", "aé", 2, "a"},
		{"three-byte rune split one in", "a世", 2, "a"},
		{"three-byte rune split two in", "a世", 3, "a"},
		{"boundary exact", "a世", 4, "a世"},
		{"first rune too big", "世", 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "f.md")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			block, err := readFileBlock(path, tt.limit)
			if err != nil {
				t.Fatalf("readFileBlock: %v", err)
			}
			if !utf8.ValidString(block.Content) {
				t.Errorf("content %q is not valid UTF-8", block.Content)
			}
			if block.Content != tt.want {
				t.Errorf("content = %q, want %q", block.Content, tt.want)
			}
			if block.ByteSize != len(tt.want) {
				t.Errorf("ByteSize = %d, want %d", block.ByteSize, len(tt.want))
			}
		})
	}
}
