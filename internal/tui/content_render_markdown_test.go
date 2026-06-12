package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestRenderPendingSteerSegment(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
		check func(t *testing.T, output string)
	}{
		{
			name:  "normal width short text",
			text:  "hello",
			width: 60,
			check: func(t *testing.T, output string) {
				// Top corners come from the manually-constructed titled border.
				if !strings.Contains(output, "\u256d") {
					t.Errorf("output missing top-left corner \u256d")
				}
				if !strings.Contains(output, "\u256e") {
					t.Errorf("output missing top-right corner \u256e")
				}
				// Bottom corners come from lipgloss.NormalBorder().
				if !strings.Contains(output, "\u2514") {
					t.Errorf("output missing bottom-left corner \u2514")
				}
				if !strings.Contains(output, "\u2518") {
					t.Errorf("output missing bottom-right corner \u2518")
				}
				if !strings.Contains(output, "queued") {
					t.Errorf("output missing 'queued'")
				}
				if !strings.Contains(output, "will send when model is ready") {
					t.Errorf("output missing title text")
				}
				if !strings.Contains(output, "hello") {
					t.Errorf("output missing segment text")
				}
				lines := strings.Split(output, "\n")
				if len(lines) < 5 {
					t.Errorf("output has %d lines, want at least 5 (top border + pad + text + pad + bottom)", len(lines))
				}
			},
		},
		{
			name:  "normal width wraps to multiple lines",
			text:  strings.Repeat("word ", 20), // 100 chars
			width: 40,
			check: func(t *testing.T, output string) {
				if !strings.Contains(output, "\u256d") {
					t.Errorf("output missing top-left corner")
				}
				if !strings.Contains(output, "queued") {
					t.Errorf("output missing 'queued'")
				}
				// Count vertical bars (│) — each content line has left+right border.
				// With padding, at least 2 content lines => ≥ 4 │ chars.
				barCount := strings.Count(output, "\u2502")
				if barCount < 4 {
					t.Errorf("output has %d vertical bars, want >= 4 (at least 2 content lines)", barCount)
				}
			},
		},
		{
			name:  "narrow viewport falls back to simple line",
			text:  "hello",
			width: 12,
			check: func(t *testing.T, output string) {
				if !strings.Contains(output, "queued:") {
					t.Errorf("output missing 'queued:' prefix")
				}
				if strings.Contains(output, "\u256d") {
					t.Errorf("narrow output should not contain box corner")
				}
				if strings.Contains(output, "\u2502") {
					t.Errorf("narrow output should not contain vertical border")
				}
			},
		},
		{
			name:  "trailing blank line preserved",
			text:  "hello",
			width: 60,
			check: func(t *testing.T, output string) {
				// The render function returns box + "\n". String() strips trailing
				// newlines before joining segments, but the bottom border proves
				// the box closed properly and the trailing blank line contract is
				// satisfied at the segment level.
				if !strings.Contains(output, "\u2514") {
					t.Errorf("output missing bottom-left corner \u2514")
				}
				if !strings.Contains(output, "\u2518") {
					t.Errorf("output missing bottom-right corner \u2518")
				}
				// Also verify that a raw round-trip through the render function
				// produces a trailing newline.
				b := newTestBuffer(t)
				seg := contentSegment{kind: segmentPendingSteer, text: "hello"}
				raw := b.renderPendingSteerSegment(seg, 60)
				if !strings.HasSuffix(raw, "\n") {
					t.Errorf("raw segment render does not end with newline")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lipgloss.SetColorProfile(termenv.Ascii)
			b := newTestBuffer(t)
			b.segments = append(b.segments, contentSegment{kind: segmentPendingSteer, text: tt.text})
			output := b.String(tt.width)
			tt.check(t, output)
		})
	}
}
