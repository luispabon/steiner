package builtin

import (
	"strings"
	"testing"
)

func TestLooksLikeNavigation(t *testing.T) {
	tests := []struct {
		name     string
		markdown string
		want     bool
	}{
		{
			name:     "empty content",
			markdown: "",
			want:     false,
		},
		{
			name:     "short content below minimum length is never flagged",
			markdown: "[a](https://example.com/a) [b](https://example.com/b)",
			want:     false,
		},
		{
			name:     "wall of navigation links",
			markdown: strings.Repeat("[Documentation Overview Page](https://example.com/docs/overview)\n", 30),
			want:     true,
		},
		{
			name: "article prose with an occasional link",
			markdown: strings.Repeat(
				"This paragraph describes the subject in plain prose sentences with real detail. ", 8,
			) + "\n\nSee also [the source material](https://example.com/source) for more context.\n" +
				strings.Repeat(
					"Another prose paragraph continues explaining the topic at reasonable length. ", 8,
				),
			want: false,
		},
		{
			// Link density alone (30 links vs. 10 prose lines) clears
			// navLinkDensityThreshold, so the only thing saving this content
			// from a "navigation" classification is the prose-line guard in
			// looksLikeNavigation: if navMaxProseLines were broken (e.g.
			// removed), this case would be misclassified as navigation.
			name: "link-dense but with substantial prose lines is not flagged",
			markdown: strings.Repeat("[Nav Item](https://example.com/x)\n", 30) +
				strings.Repeat(
					"A long line of genuine prose content that easily clears the per-line threshold. \n", 10,
				),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := looksLikeNavigation(tt.markdown)
			if got != tt.want {
				t.Errorf("looksLikeNavigation(%q) = %v, want %v", tt.markdown, got, tt.want)
			}
		})
	}
}
