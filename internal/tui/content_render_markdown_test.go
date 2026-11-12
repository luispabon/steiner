package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
)

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
			want: "09:07:00",
		},
		{
			name: "older",
			ts:   time.Date(2026, 6, 18, 9, 7, 0, 0, time.UTC),
			want: "Jun 18 09:07:00",
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
			wantTimestamp: "15:04:00",
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
			wantTimestamp: "Jun 18 09:07:00",
			wantText:      "Title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newTestBuffer(t)
			useTrueColor(t)
			rendered := tt.render(b, tt.segment, 60)
			rawLines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
			if len(rawLines) < 4 {
				t.Fatalf("rendered output has %d raw lines, want at least 4: %q", len(rawLines), rendered)
			}
			if !hasANSIBackground(rawLines[0]) {
				t.Fatalf("top row lost prompt background styling: %q", rawLines[0])
			}
			if !hasANSIBackground(rawLines[len(rawLines)-2]) {
				t.Fatalf("bottom row lost prompt background styling: %q", rawLines[len(rawLines)-2])
			}

			plain := stripANSI(rendered)
			lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
			if len(lines) < 4 {
				t.Fatalf("rendered output has %d lines, want at least 4: %q", len(lines), plain)
			}
			if strings.Contains(lines[0], tt.wantTimestamp) {
				t.Fatalf("top row %q unexpectedly contains timestamp %q", lines[0], tt.wantTimestamp)
			}
			if strings.Contains(lines[0], tt.wantText) {
				t.Fatalf("top row %q unexpectedly contains message text %q", lines[0], tt.wantText)
			}
			if strings.Contains(lines[len(lines)-2], tt.wantTimestamp) {
				t.Fatalf("bottom row %q unexpectedly contains timestamp %q", lines[len(lines)-2], tt.wantTimestamp)
			}
			if strings.Contains(lines[len(lines)-1], "┃") {
				t.Fatalf("timestamp row %q unexpectedly contains prompt border", lines[len(lines)-1])
			}
			if got := strings.TrimSpace(lines[len(lines)-1]); got != tt.wantTimestamp {
				t.Fatalf("timestamp row = %q, want right-aligned timestamp %q", lines[len(lines)-1], tt.wantTimestamp)
			}
			if !strings.Contains(plain, tt.wantText) {
				t.Fatalf("rendered output missing message text %q: %q", tt.wantText, plain)
			}
		})
	}
}

// eventAt returns event stamped with ts, as replay does for a persisted
// message time.
func eventAt(event output.Event, ts time.Time) output.Event {
	event.Timestamp = ts
	return event
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

	historical := time.Date(2026, 6, 10, 18, 11, 4, 0, time.UTC)
	tests := []struct {
		name string
		run  func(*contentBuffer)
		want time.Time
	}{
		{
			name: "AppendUser stamps now",
			run: func(b *contentBuffer) {
				b.AppendUser("hello")
			},
			want: fixedNow,
		},
		{
			name: "appendUserInputEvent keeps event timestamp",
			run: func(b *contentBuffer) {
				b.appendUserInputEvent(eventAt(output.NewUserInputEvent("hello", "exec", nil), historical))
			},
			want: historical,
		},
		{
			name: "appendStopReasonEvent keeps event timestamp",
			run: func(b *contentBuffer) {
				b.appendStopReasonEvent(eventAt(output.NewStopReasonEvent(1, "complete", nil), historical))
			},
			want: historical,
		},
		{
			name: "appendSubAgentsDeliveredEvent keeps event timestamp",
			run: func(b *contentBuffer) {
				b.appendSubAgentsDeliveredEvent(eventAt(output.NewSubAgentsDeliveredEvent([]output.DeliveredSubAgent{
					{AgentID: "child", AgentType: "explore", Status: "complete"},
				}), historical))
			},
			want: historical,
		},
		{
			name: "appendSubAgentResultsUnansweredEvent keeps event timestamp",
			run: func(b *contentBuffer) {
				b.appendSubAgentResultsUnansweredEvent(eventAt(output.NewSubAgentResultsUnansweredEvent([]string{"child"}, "boom"), historical))
			},
			want: historical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newTestBuffer(t)
			tt.run(b)
			if len(b.segments) != 1 {
				t.Fatalf("segments = %d, want 1", len(b.segments))
			}
			if got := b.segments[0].timestamp; !got.Equal(tt.want) {
				t.Fatalf("segment timestamp = %v, want %v", got, tt.want)
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
		wantText      string
		wantTimestamp bool
	}{
		{
			name:          "complete",
			event:         eventAt(output.NewStopReasonEvent(1, "complete", nil), fixedNow),
			wantText:      "status · complete",
			wantTimestamp: true,
		},
		{
			name:          "end_turn",
			event:         eventAt(output.NewStopReasonEvent(1, "end_turn", nil), fixedNow),
			wantText:      "status · end_turn",
			wantTimestamp: true,
		},
		{
			name:          "cancelled",
			event:         eventAt(output.NewStopReasonEvent(1, "cancelled", nil), fixedNow),
			wantText:      "status · cancelled",
			wantTimestamp: false,
		},
		{
			name:          "error",
			event:         eventAt(output.NewStopReasonEvent(1, "error", errors.New("boom")), fixedNow),
			wantText:      "✗ boom",
			wantTimestamp: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newTestBuffer(t)
			b.appendStopReasonEvent(tt.event)
			plain := stripANSI(b.String(60))
			if !strings.Contains(plain, tt.wantText) {
				t.Fatalf("rendered output missing %q: %q", tt.wantText, plain)
			}
			hasTimestamp := strings.Contains(plain, ts)
			if hasTimestamp != tt.wantTimestamp {
				t.Fatalf("timestamp presence = %v, want %v in %q", hasTimestamp, tt.wantTimestamp, plain)
			}
			if tt.wantTimestamp {
				if strings.Index(plain, tt.wantText) > strings.Index(plain, ts) {
					t.Fatalf("timestamp %q appeared before stop reason %q in %q", ts, tt.wantText, plain)
				}
			}
		})
	}
}

// TestZeroEventTimestampRendersNoTimestampChrome covers replayed sessions whose
// events carry no message time: the row must render without a timestamp or the
// blank margin row that precedes one.
func TestZeroEventTimestampRendersNoTimestampChrome(t *testing.T) {
	originalTimeNow := timeNow
	fixedNow := time.Date(2026, 6, 19, 15, 4, 0, 0, time.UTC)
	timeNow = func() time.Time {
		return fixedNow
	}
	t.Cleanup(func() {
		timeNow = originalTimeNow
	})

	tests := []struct {
		name   string
		apply  func(*contentBuffer)
		render func(*contentBuffer, contentSegment, int) string
	}{
		{
			name: "user input",
			apply: func(b *contentBuffer) {
				b.appendUserInputEvent(eventAt(output.NewUserInputEvent("hello", "exec", nil), time.Time{}))
			},
			render: (*contentBuffer).renderUserSegment,
		},
		{
			name: "stop reason",
			apply: func(b *contentBuffer) {
				b.appendStopReasonEvent(eventAt(output.NewStopReasonEvent(1, "complete", nil), time.Time{}))
			},
			render: (*contentBuffer).renderStatusSegment,
		},
		{
			name: "sub-agents delivered",
			apply: func(b *contentBuffer) {
				b.appendSubAgentsDeliveredEvent(eventAt(output.NewSubAgentsDeliveredEvent([]output.DeliveredSubAgent{
					{AgentID: "child", AgentType: "explore", Status: "complete"},
				}), time.Time{}))
			},
			render: (*contentBuffer).renderSubAgentsFinishedSegment,
		},
		{
			name: "results unanswered",
			apply: func(b *contentBuffer) {
				b.appendSubAgentResultsUnansweredEvent(eventAt(output.NewSubAgentResultsUnansweredEvent([]string{"child"}, "boom"), time.Time{}))
			},
			render: (*contentBuffer).renderStrandedResultsSegment,
		},
	}

	wantNothing := []string{"15:04:00", "Jan 01"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newTestBuffer(t)
			tt.apply(b)
			if len(b.segments) != 1 {
				t.Fatalf("segments = %d, want 1", len(b.segments))
			}
			segment := b.segments[0]
			if !segment.timestamp.IsZero() {
				t.Fatalf("segment timestamp = %v, want zero", segment.timestamp)
			}
			plain := stripANSI(tt.render(b, segment, 60))
			if strings.HasPrefix(plain, "\n") {
				t.Errorf("rendered row starts with a blank timestamp margin: %q", plain)
			}
			for _, want := range wantNothing {
				if strings.Contains(plain, want) {
					t.Errorf("rendered row %q unexpectedly contains timestamp %q", plain, want)
				}
			}
		})
	}
}

func TestAssistantMarkdownWrapsLongProseLines(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	useTrueColor(t)

	// Long single-line, marker-free prose that must wrap at a modest width.
	longText := "Question 1 of 8: For the AI queries, do you want the LLM processing to be fully local with no document contents sent to external providers, or are you comfortable sending content to a cloud API for inference"
	b.appendMarkdownBlock(longText)

	if len(b.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(b.segments))
	}
	if seg := b.segments[0]; seg.kind != segmentAssistantMarkdown {
		t.Fatalf("segment kind = %v, want segmentAssistantMarkdown", seg.kind)
	}

	rendered := b.String(40)
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	if len(lines) <= 1 {
		t.Fatalf("rendered output has %d lines, want > 1: %q", len(lines), rendered)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w > 40 {
			t.Errorf("line %d width = %d, want <= 40: %q", i, w, line)
		}
	}

	plain := stripANSI(rendered)
	if !strings.Contains(plain, "Question 1 of 8") {
		t.Errorf("rendered output missing start of text: %q", plain)
	}
	if !strings.Contains(plain, "external providers") {
		t.Errorf("rendered output missing part of text: %q", plain)
	}
}
