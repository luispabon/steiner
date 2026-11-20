package tui

import (
	"testing"
	"unicode/utf8"
)

func TestFormatTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		n    int
		want string
	}{
		{"zero", 0, "-"},
		{"one", 1, "1"},
		{"largest raw", 999, "999"},
		{"one k", 1_000, "1.0k"},
		{"k rounds down", 1_049, "1.0k"},
		{"k rounds half up", 1_050, "1.1k"},
		{"k with decimal", 498_400, "498.4k"},
		{"k below promotion", 999_949, "999.9k"},
		{"k promotes to M", 999_950, "1.0M"},
		{"one M", 1_000_000, "1.0M"},
		{"M with decimal", 117_300_000, "117.3M"},
		{"M below promotion", 999_949_999, "999.9M"},
		{"M promotes to B", 999_950_000, "1.0B"},
		{"B with decimal", 4_694_000_000, "4.7B"},
		{"negative", -5, "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := formatTokens(tt.n); got != tt.want {
				t.Fatalf("formatTokens(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

func TestFormatPercent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		rate float64
		ok   bool
		want string
	}{
		{"undefined", 0.937, false, "-"},
		{"zero", 0, true, "0.0%"},
		{"half", 0.5, true, "50.0%"},
		{"one decimal", 0.937, true, "93.7%"},
		{"full", 1, true, "100.0%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := formatPercent(tt.rate, tt.ok); got != tt.want {
				t.Fatalf("formatPercent(%v, %v) = %q, want %q", tt.rate, tt.ok, got, tt.want)
			}
		})
	}
}

func TestBarString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		value, total int
		width        int
		want         string
	}{
		{"zero value", 0, 100, 10, "░░░░░░░░░░"},
		{"half", 50, 100, 10, "█████░░░░░"},
		{"full", 100, 100, 10, "██████████"},
		{"tiny value keeps one cell", 1, 1_000, 10, "█░░░░░░░░░"},
		{"rounds half up", 1, 8, 4, "█░░░"},
		{"rounds half up to full", 7, 8, 4, "████"},
		{"value above total clamps", 200, 100, 5, "█████"},
		{"zero total", 5, 0, 4, "░░░░"},
		{"negative total", 5, -100, 4, "░░░░"},
		{"negative value", -5, 100, 4, "░░░░"},
		{"zero width", 5, 100, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := barString(tt.value, tt.total, tt.width)
			if got != tt.want {
				t.Fatalf("barString(%d, %d, %d) = %q, want %q", tt.value, tt.total, tt.width, got, tt.want)
			}
			if width := utf8.RuneCountInString(got); width != tt.width {
				t.Fatalf("barString(%d, %d, %d) width = %d, want %d", tt.value, tt.total, tt.width, width, tt.width)
			}
		})
	}
}
