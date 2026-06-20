package interactive

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestClassifyContextReportDisplay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "short single line returns inline",
			content: "cave_human mode: on",
			want:    output.ContextReportDisplayInline,
		},
		{
			name:    "empty content returns inline",
			content: "",
			want:    output.ContextReportDisplayInline,
		},
		{
			name:    "exactly 100 chars returns inline",
			content: strings.Repeat("x", 100),
			want:    output.ContextReportDisplayInline,
		},
		{
			name:    "101 chars returns overlay",
			content: strings.Repeat("x", 101),
			want:    output.ContextReportDisplayOverlay,
		},
		{
			name:    "multi-line returns overlay",
			content: "line one\nline two",
			want:    output.ContextReportDisplayOverlay,
		},
		{
			name:    "short multi-line returns overlay",
			content: "a\nb",
			want:    output.ContextReportDisplayOverlay,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyContextReportDisplay(tt.content)
			if got != tt.want {
				t.Errorf("ClassifyContextReportDisplay(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}
