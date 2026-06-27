package tui

import (
	"strings"
	"testing"
)

// renderViewportWithScrollbarOriginal is the pre-optimization reference implementation
// kept here to validate output parity with the builder-based replacement.
func renderViewportWithScrollbarOriginal(viewportInner, scrollbar string) string {
	vpLines := strings.Split(viewportInner, "\n")
	scLines := strings.Split(scrollbar, "\n")
	merged := make([]string, 0, len(vpLines)+1)
	merged = append(merged, "")
	for i := 0; i < len(vpLines) && i < len(scLines); i++ {
		merged = append(merged, vpLines[i]+scLines[i])
	}
	return strings.Join(merged, "\n")
}

func TestRenderViewportWithScrollbar(t *testing.T) {
	m := Model{}

	cases := []struct {
		name      string
		viewport  string
		scrollbar string
	}{
		{
			name:      "empty both",
			viewport:  "",
			scrollbar: "",
		},
		{
			name:      "single line equal length",
			viewport:  "hello",
			scrollbar: "|",
		},
		{
			name:      "multiline equal",
			viewport:  "line1\nline2\nline3",
			scrollbar: "a\nb\nc",
		},
		{
			name:      "scrollbar shorter than viewport",
			viewport:  "line1\nline2\nline3",
			scrollbar: "a\nb",
		},
		{
			name:      "scrollbar longer than viewport",
			viewport:  "line1\nline2",
			scrollbar: "a\nb\nc",
		},
		{
			name:      "viewport empty scrollbar non-empty",
			viewport:  "",
			scrollbar: "bar",
		},
		{
			name:      "ansi escape sequences",
			viewport:  "\x1b[31mred\x1b[0m\nplain",
			scrollbar: "\x1b[32m|\x1b[0m\n ",
		},
		{
			name:      "trailing newline in viewport",
			viewport:  "a\nb\n",
			scrollbar: "x\ny\nz",
		},
		{
			name:      "trailing newline in both",
			viewport:  "a\nb\n",
			scrollbar: "x\ny\n",
		},
		{
			name:      "single line viewport no scrollbar",
			viewport:  "onlyme",
			scrollbar: "",
		},
		{
			name:      "many lines",
			viewport:  strings.Repeat("content line\n", 50),
			scrollbar: strings.Repeat("│\n", 50),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := renderViewportWithScrollbarOriginal(tc.viewport, tc.scrollbar)
			got := m.renderViewportWithScrollbar(tc.viewport, tc.scrollbar)
			if got != want {
				t.Errorf("output mismatch\nwant: %q\n got: %q", want, got)
			}
		})
	}
}
