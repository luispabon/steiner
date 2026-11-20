package tui

import (
	"testing"
	"unicode/utf8"
)

func TestRenderBanner(t *testing.T) {
	t.Parallel()
	t.Run("five equal rows", func(t *testing.T) {
		t.Parallel()
		rows := renderBanner("84.2M")
		if len(rows) != bannerRows {
			t.Fatalf("len(rows) = %d, want %d", len(rows), bannerRows)
		}
		want := bannerWidth("84.2M")
		for i, row := range rows {
			if got := utf8.RuneCountInString(row); got != want {
				t.Fatalf("row %d width = %d, want %d (%q)", i, got, want, row)
			}
		}
	})

	t.Run("golden 84.2M", func(t *testing.T) {
		t.Parallel()
		want := []string{
			" ███   █   █      ███   █   █",
			"█   █  █   █         █  ██ ██",
			" ███   █████      ███   █ █ █",
			"█   █      █     █      █   █",
			" ███       █  █  █████  █   █",
		}
		got := renderBanner("84.2M")
		if len(got) != len(want) {
			t.Fatalf("len(rows) = %d, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("row %d = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("unknown runes skipped", func(t *testing.T) {
		t.Parallel()
		got := renderBanner("?8x4!")
		want := renderBanner("84")
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("row %d = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("empty text", func(t *testing.T) {
		t.Parallel()
		rows := renderBanner("")
		if len(rows) != bannerRows {
			t.Fatalf("len(rows) = %d, want %d", len(rows), bannerRows)
		}
		for i, row := range rows {
			if row != "" {
				t.Fatalf("row %d = %q, want empty", i, row)
			}
		}
	})
}

func TestBannerWidth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want int
	}{
		{"empty", "", 0},
		{"digit", "8", 5},
		{"dot", ".", 1},
		{"two glyphs", "84", 12},
		{"amount", "84.2M", 29},
		{"space glyph", "8 4", 19},
		{"unknown runes only", "?", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := bannerWidth(tt.text); got != tt.want {
				t.Fatalf("bannerWidth(%q) = %d, want %d", tt.text, got, tt.want)
			}
			rows := renderBanner(tt.text)
			for i, row := range rows {
				if got := utf8.RuneCountInString(row); got != tt.want {
					t.Fatalf("row %d of %q width = %d, want %d", i, tt.text, got, tt.want)
				}
			}
		})
	}
}

func TestBannerGlyphsRectangular(t *testing.T) {
	t.Parallel()
	for r, glyph := range bannerGlyphs {
		want := utf8.RuneCountInString(glyph[0])
		if r != '.' && want != 5 {
			t.Errorf("glyph %q width = %d, want 5", r, want)
		}
		for i, row := range glyph {
			if got := utf8.RuneCountInString(row); got != want {
				t.Errorf("glyph %q row %d width = %d, want %d", r, i, got, want)
			}
		}
	}
}
