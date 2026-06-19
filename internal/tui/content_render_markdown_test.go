package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/luispabon/steiner/internal/output"
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

func TestTimestampFormat(t *testing.T) {
	originalTimeNow := timeNow
	fixedNow := time.Date(2026, 6, 19, 15, 4, 0, 0, time.UTC)
	timeNow = func() time.Time {
		return fixedNow
	}
	t.Cleanup(func() {
		timeNow = originalTimeNow
	})

	tests := []struct {
		name string
		ts   time.Time
		want string
	}{
		{
			name: "today",
			ts:   time.Date(2026, 6, 19, 9, 7, 0, 0, time.UTC),
			want: "09:07",
		},
		{
			name: "older",
			ts:   time.Date(2026, 6, 18, 9, 7, 0, 0, time.UTC),
			want: "Jun 18 09:07",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatContentTimestamp(tt.ts); got != tt.want {
				t.Fatalf("formatContentTimestamp() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderUserTimestampChrome(t *testing.T) {
	originalTimeNow := timeNow
	fixedNow := time.Date(2026, 6, 19, 15, 4, 0, 0, time.UTC)
	timeNow = func() time.Time {
		return fixedNow
	}
	t.Cleanup(func() {
		timeNow = originalTimeNow
	})

	tests := []struct {
		name          string
		segment       contentSegment
		render        func(*contentBuffer, contentSegment, int) string
		wantTimestamp string
		wantText      string
	}{
		{
			name: "plain user segment",
			segment: contentSegment{
				kind:      segmentUser,
				text:      "hello world",
				timestamp: fixedNow,
			},
			render:        (*contentBuffer).renderUserSegment,
			wantTimestamp: "15:04",
			wantText:      "hello world",
		},
		{
			name: "markdown user segment",
			segment: contentSegment{
				kind:      segmentUserMarkdown,
				text:      "# Title\n\ncontent",
				timestamp: time.Date(2026, 6, 18, 9, 7, 0, 0, time.UTC),
			},
			render:        (*contentBuffer).renderUserMarkdownSegment,
			wantTimestamp: "Jun 18 09:07",
			wantText:      "Title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newTestBuffer(t)
			plain := stripANSI(tt.render(b, tt.segment, 60))
			lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
			if len(lines) < 3 {
				t.Fatalf("rendered output has %d lines, want at least 3: %q", len(lines), plain)
			}
			if !strings.Contains(lines[0], tt.wantTimestamp) {
				t.Fatalf("top row %q missing timestamp %q", lines[0], tt.wantTimestamp)
			}
			if strings.Contains(lines[0], tt.wantText) {
				t.Fatalf("top row %q unexpectedly contains message text %q", lines[0], tt.wantText)
			}
			if !strings.Contains(plain, tt.wantText) {
				t.Fatalf("rendered output missing message text %q: %q", tt.wantText, plain)
			}
		})
	}
}

func TestTimestampCapture(t *testing.T) {
	originalTimeNow := timeNow
	fixedNow := time.Date(2026, 6, 19, 15, 4, 0, 0, time.UTC)
	timeNow = func() time.Time {
		return fixedNow
	}
	t.Cleanup(func() {
		timeNow = originalTimeNow
	})

	tests := []struct {
		name string
		run  func(*contentBuffer)
	}{
		{
			name: "AppendUser",
			run: func(b *contentBuffer) {
				b.AppendUser("hello")
			},
		},
		{
			name: "appendUserInputEvent",
			run: func(b *contentBuffer) {
				b.appendUserInputEvent(output.NewUserInputEvent("hello", "exec"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newTestBuffer(t)
			tt.run(b)
			if len(b.segments) != 1 {
				t.Fatalf("segments = %d, want 1", len(b.segments))
			}
			if got := b.segments[0].timestamp; !got.Equal(fixedNow) {
				t.Fatalf("segment timestamp = %v, want %v", got, fixedNow)
			}
		})
	}
}

func TestStopReasonTimestampFiltering(t *testing.T) {
	originalTimeNow := timeNow
	fixedNow := time.Date(2026, 6, 19, 15, 4, 0, 0, time.UTC)
	timeNow = func() time.Time {
		return fixedNow
	}
	t.Cleanup(func() {
		timeNow = originalTimeNow
	})

	ts := formatContentTimestamp(fixedNow)
	tests := []struct {
		name          string
		event         output.Event
		wantTimestamp bool
	}{
		{
			name:          "complete",
			event:         output.NewStopReasonEvent(1, "complete", nil),
			wantTimestamp: true,
		},
		{
			name:          "end_turn",
			event:         output.NewStopReasonEvent(1, "end_turn", nil),
			wantTimestamp: true,
		},
		{
			name:          "cancelled",
			event:         output.NewStopReasonEvent(1, "cancelled", nil),
			wantTimestamp: false,
		},
		{
			name:          "error",
			event:         output.NewStopReasonEvent(1, "error", errors.New("boom")),
			wantTimestamp: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newTestBuffer(t)
			b.appendStopReasonEvent(tt.event)
			plain := stripANSI(b.String(60))
			hasTimestamp := strings.Contains(plain, ts)
			if hasTimestamp != tt.wantTimestamp {
				t.Fatalf("timestamp presence = %v, want %v in %q", hasTimestamp, tt.wantTimestamp, plain)
			}
		})
	}
}
