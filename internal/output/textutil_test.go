package output

import (
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
		{"hello world", 0, "hello world"},
		{"hello world", -1, "hello world"},
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
		{"a", 0, "a"},
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
