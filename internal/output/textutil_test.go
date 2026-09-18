package output

import (
	"strings"
	"testing"
)

func TestTruncateWithEllipsis(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		// Basic cases
		{"hello world", 11, "hello world"},
		{"hello world", 12, "hello world"},
		{"hello world", 10, "hello w..."},
		{"hello world", 20, "hello world"},

		// Boundary cases
		{"hello world", 0, ""},
		{"hello world", -1, ""},
		{"hello world", 1, "h"},
		{"hello world", 3, "hel"},
		{"hello world", 4, "h..."},

		// Whitespace normalization
		{"  hello   world  ", 20, "hello world"},
		{"hello\n\nworld", 20, "hello world"},
		{"hello\t\tworld", 20, "hello world"},

		// Empty and short strings
		{"", 10, ""},
		{"a", 1, "a"},
		{"a", 0, ""},
		{"ab", 3, "ab"},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},

		// Various truncation points
		{"abcdefghij", 7, "abcd..."},
		{"abcdefghij", 8, "abcde..."},
		{"abcdefghij", 9, "abcdef..."},
		{"abcdefghij", 10, "abcdefghij"},
		{"abcdefghij", 11, "abcdefghij"},

		// Whitespace in string
		{"a b c d e f g h", 10, "a b c d..."},
		{"one two three four", 15, "one two thre..."},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := TruncateWithEllipsis(tt.input, tt.maxLen)
			if result != tt.want {
				t.Fatalf("TruncateWithEllipsis(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.want)
			}
		})
	}
}

func TestTruncateWithEllipsisSmallMaxLens(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello world", 1, "h"},
		{"hello world", 2, "he"},
		{"hello world", 3, "hel"},
		{"test", 1, "t"},
		{"test", 2, "te"},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := TruncateWithEllipsis(tt.input, tt.maxLen)
			if result != tt.want {
				t.Fatalf("TruncateWithEllipsis(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.want)
			}
		})
	}
}

func TestPluralSuffix(t *testing.T) {
	tests := []struct {
		count    int
		singular string
		plural   string
		want     string
	}{
		{1, "file", "files", "file"},
		{0, "file", "files", "files"},
		{2, "file", "files", "files"},
		{10, "turn", "turns", "turns"},
		{1, "turn", "turns", "turn"},
		{1, "", "s", ""},
		{2, "", "s", "s"},
		{1, "message", "", "message"},
		{2, "message", "", ""},
		{100, "x", "y", "y"},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := PluralSuffix(tt.count, tt.singular, tt.plural)
			if result != tt.want {
				t.Fatalf("PluralSuffix(%d, %q, %q) = %q, want %q", tt.count, tt.singular, tt.plural, result, tt.want)
			}
		})
	}
}

func TestTruncateWithEllipsisPreservesContent(t *testing.T) {
	tests := []struct {
		input   string
		maxLen  int
		checkFn func(t *testing.T, result string)
	}{
		{
			input:  "a b c d e f",
			maxLen: 10,
			checkFn: func(t *testing.T, result string) {
				if len(result) > 10 {
					t.Errorf("result length %d exceeds maxLen 10", len(result))
				}
			},
		},
		{
			input:  "hello     world",
			maxLen: 20,
			checkFn: func(t *testing.T, result string) {
				if result != "hello world" {
					t.Errorf("multiple spaces not normalized: %q", result)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := TruncateWithEllipsis(tt.input, tt.maxLen)
			tt.checkFn(t, result)
		})
	}
}

func TestTruncateWithEllipsisUTF8(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		// Multi-byte UTF-8 characters (each 日 is 3 bytes, 1 rune)
		// maxLen=5: (5-3 for "...")=2 chars + "..." = 5 total
		{"日本語テスト", 5, "日本..."},
		// maxLen=3: no ellipsis, just 3 chars
		{"日本語テスト", 3, "日本語"},
		// maxLen=5: (5-3)=2 chars + "..."
		{"こんにちは世界", 5, "こん..."},

		// Emoji (most are 4 bytes, 1 rune in Go)
		// maxLen=10: "Hello 👋 " is 9 runes, fits without truncation
		{"Hello 👋 World", 10, "Hello 👋..."},
		// maxLen=3: no ellipsis
		{"👋👋👋👋👋", 3, "👋👋👋"},
		// maxLen=5: fits without truncation
		{"👋👋👋👋👋", 5, "👋👋👋👋👋"},

		// Mixed ASCII and multi-byte
		// "Hello 世界" is 8 runes, fits at maxLen=8
		{"Hello 世界", 8, "Hello 世界"},
		// "Hello 世界" is 8 runes, maxLen=6: 3 runes + "..."
		{"Hello 世界", 6, "Hel..."},
		// "Test 日本 World" is 14 runes; at maxLen=8: 5 runes + "..."
		{"Test 日本 World", 8, "Test ..."},

		// Truncation with many repeated runes
		// 100 日 chars, maxLen=100, fits without truncation
		{strings.Repeat("日", 100), 100, strings.Repeat("日", 100)},
		// 100 日 chars, maxLen=75: (75-3)=72 + "..."
		{strings.Repeat("日", 100), 75, strings.Repeat("日", 72) + "..."},

		// Edge case: string exactly at limit
		{"Café", 4, "Café"},
		// maxLen=3: no ellipsis
		{"Café", 3, "Caf"},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := TruncateWithEllipsis(tt.input, tt.maxLen)
			if result != tt.want {
				t.Fatalf("TruncateWithEllipsis(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.want)
			}
		})
	}
}
