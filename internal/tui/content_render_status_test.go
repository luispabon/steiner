package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

func TestAppendLineRoutesStatusPrefixToStatusSegment(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{segments: make([]contentSegment, 0)}

	b.appendLine("status: model switched to opencode-go/minimax-m3")

	if len(b.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(b.segments))
	}
	seg := b.segments[0]
	if seg.kind != segmentStatus {
		t.Fatalf("segment kind = %v, want segmentStatus", seg.kind)
	}
	if seg.text != "model switched to opencode-go/minimax-m3" {
		t.Fatalf("segment text = %q, want body without prefix", seg.text)
	}
}

func TestAppendLineKeepsNonStatusLinesAsPlain(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{segments: make([]contentSegment, 0)}

	b.appendLine("note")
	b.appendLine("status: run started") // suppressed by shouldSuppressLine
	b.appendLine("")

	if len(b.segments) != 1 {
		t.Fatalf("segments count = %d, want 1 (only the plain note)", len(b.segments))
	}
	seg := b.segments[0]
	if seg.kind != segmentPlain {
		t.Fatalf("segment kind = %v, want segmentPlain", seg.kind)
	}
	if seg.text != "note" {
		t.Fatalf("segment text = %q, want %q", seg.text, "note")
	}
}

func TestRenderStatusSegmentBasic(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	seg := contentSegment{kind: segmentStatus, text: "model switched to opencode-go/minimax-m3"}

	out := b.renderStatusSegment(seg, 80)

	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output does not end with newline: %q", out)
	}
	if !strings.Contains(out, "status") {
		t.Errorf("output missing 'status' tag: %q", out)
	}
	if !strings.Contains(out, "model switched to opencode-go/minimax-m3") {
		t.Errorf("output missing body: %q", out)
	}
	if !strings.Contains(out, "○") {
		t.Errorf("output missing bullet glyph: %q", out)
	}
	if !strings.Contains(out, "·") {
		t.Errorf("output missing middle-dot separator: %q", out)
	}
}

func TestRenderStatusSegmentTimestampInlineRightAligned(t *testing.T) {
	originalTimeNow := timeNow
	fixedNow := time.Date(2026, 6, 19, 19, 54, 0, 0, time.UTC)
	timeNow = func() time.Time {
		return fixedNow
	}
	t.Cleanup(func() {
		timeNow = originalTimeNow
	})

	b := newTestBuffer(t)
	seg := contentSegment{
		kind:      segmentStatus,
		text:      "complete",
		timestamp: fixedNow,
	}

	plain := stripANSI(b.renderStatusSegment(seg, 40))
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("rendered status lines = %d, want 2: %q", len(lines), plain)
	}
	if lines[0] != "" {
		t.Fatalf("top margin row = %q, want blank", lines[0])
	}
	if !strings.Contains(lines[1], "status · complete") {
		t.Fatalf("status row missing body: %q", lines[1])
	}
	if !strings.HasSuffix(lines[1], "19:54:00") {
		t.Fatalf("timestamp is not right-aligned at row end: %q", lines[1])
	}
}

func TestRenderStatusSegmentStripsTrailingDot(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	seg := contentSegment{kind: segmentStatus, text: "session restored."}

	out := b.renderStatusSegment(seg, 80)

	// Body should appear once, not "session restored. ·".
	if strings.Contains(out, "session restored. ·") {
		t.Errorf("output has double separator: %q", out)
	}
	if !strings.Contains(out, "session restored.") {
		t.Errorf("output missing body: %q", out)
	}
}

func TestRenderStatusSegmentStripsTrailingBullet(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	// Some callers already include the separator dot in the body.
	seg := contentSegment{kind: segmentStatus, text: "session restored ·"}

	out := b.renderStatusSegment(seg, 80)

	if strings.Count(out, "·") > 1 {
		t.Errorf("output has more than one separator: %q", out)
	}
}

func TestRenderStatusSegmentEmptyBody(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	seg := contentSegment{kind: segmentStatus, text: ""}

	out := b.renderStatusSegment(seg, 80)

	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output does not end with newline: %q", out)
	}
	if !strings.Contains(out, "status") {
		t.Errorf("output missing 'status' tag: %q", out)
	}
	// No separator when body is empty.
	if strings.Contains(out, "·") {
		t.Errorf("empty body should not render separator: %q", out)
	}
}

func TestRenderStatusSegmentNarrowWidthDoesNotPanic(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	seg := contentSegment{kind: segmentStatus, text: "model switched to opencode-go/minimax-m3"}

	// Widths smaller than the prefix should not cause a negative body width.
	for _, w := range []int{0, 1, 5, 10, 15} {
		out := b.renderStatusSegment(seg, w)
		if !strings.HasSuffix(out, "\n") {
			t.Errorf("width %d: output does not end with newline: %q", w, out)
		}
		if !strings.Contains(out, "status") {
			t.Errorf("width %d: output missing 'status' tag: %q", w, out)
		}
	}
}

func TestRenderStatusSegmentWrapsLongBody(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	body := strings.Repeat("long-token-with-no-spaces-", 8)
	seg := contentSegment{kind: segmentStatus, text: body}

	out := b.renderStatusSegment(seg, 40)

	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output does not end with newline: %q", out)
	}
	// Body should still appear, even if wrapped.
	if !strings.Contains(out, "long-token") {
		t.Errorf("output missing body content: %q", out)
	}
}

func TestRenderStatusSegmentWhitespaceOnlyBody(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	seg := contentSegment{kind: segmentStatus, text: "   "}

	out := b.renderStatusSegment(seg, 80)

	// Whitespace-only body collapses to empty, so no separator.
	if strings.Contains(out, "·") {
		t.Errorf("whitespace-only body should not render separator: %q", out)
	}
	if !strings.Contains(out, "status") {
		t.Errorf("output missing 'status' tag: %q", out)
	}
}

func TestPhaseTransitionEventRendersAsStatusSegment(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	// Use real output.NewPhaseTransitionEvent and AppendEvent dispatch
	event := output.NewPhaseTransitionEvent("test-run", "plan", "implement", "starting", "test-model", "test-session")

	b.AppendEvent(event)

	if len(b.segments) < 1 {
		t.Fatalf("segments count = %d, want at least 1", len(b.segments))
	}

	// Find the status segment
	var statusSeg *contentSegment
	for i := range b.segments {
		if b.segments[i].kind == segmentStatus {
			statusSeg = &b.segments[i]
			break
		}
	}

	if statusSeg == nil {
		t.Fatalf("no segmentStatus found in %d segments", len(b.segments))
	}

	// Verify the status text contains phase transition information
	if !strings.Contains(statusSeg.text, "plan") || !strings.Contains(statusSeg.text, "implement") {
		t.Errorf("status segment text = %q, want to contain phase names 'plan' and 'implement'", statusSeg.text)
	}
}

func TestPhaseSeparatorHasBlankLinesAboveAndBelow(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	// Create a phase separator
	seg := contentSegment{
		kind:          segmentSeparator,
		separatorData: &separatorData{label: "Phase: implement", phase: true},
		renderDirty:   true,
	}

	out := b.renderSeparatorSegment(seg, 80)

	lines := strings.Split(out, "\n")
	// The joiner supplies the leading blank line; the segment supplies its trailing newline.
	// When split by \n, we get 2 elements (including trailing empty after final newline).
	if len(lines) < 2 {
		t.Errorf("phase separator output has too few lines: %q", out)
	}
	// The segment should not add a leading newline; joinSeparator adds the margin.
	if strings.HasPrefix(out, "\n") {
		t.Errorf("phase separator should not start with a newline: %q", out)
	}
	// Should end with newline, creating blank line after
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("phase separator should end with newline: %q", out)
	}
	// The separator line should contain dashes
	if !strings.Contains(out, "implement") {
		t.Errorf("phase separator should contain label: %q", out)
	}
}

func TestNonPhaseSeparatorSpacingUnchanged(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	// Create a non-phase separator (e.g., for compaction)
	seg := contentSegment{
		kind:          segmentSeparator,
		separatorData: &separatorData{label: "Compaction", closing: false},
		renderDirty:   true,
	}

	out := b.renderSeparatorSegment(seg, 80)

	// Non-phase separators should only have newline after, not before
	if strings.HasPrefix(out, "\n") {
		t.Errorf("non-phase opening separator should not start with newline: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("non-phase separator should end with newline: %q", out)
	}
}
