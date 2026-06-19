package tui

import (
	"strings"
	"testing"
)

func TestAppendLineRoutesStatusPrefixToStatusSegment(t *testing.T) {
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

func TestRenderStatusSegmentStripsTrailingDot(t *testing.T) {
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
	b := newTestBuffer(t)
	// Some callers already include the separator dot in the body.
	seg := contentSegment{kind: segmentStatus, text: "session restored ·"}

	out := b.renderStatusSegment(seg, 80)

	if strings.Count(out, "·") > 1 {
		t.Errorf("output has more than one separator: %q", out)
	}
}

func TestRenderStatusSegmentEmptyBody(t *testing.T) {
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
	b := newTestBuffer(t)
	// Simulate what AppendEvent does with a phase transition event
	event := mockPhaseTransitionEvent()
	line := strings.TrimSpace(mockFormatEvent(event))

	b.appendStyled(line, segmentStatus)

	if len(b.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(b.segments))
	}
	seg := b.segments[0]
	if seg.kind != segmentStatus {
		t.Fatalf("segment kind = %v, want segmentStatus", seg.kind)
	}
	// Verify the text contains key phase information
	if !strings.Contains(seg.text, "phase transition") || !strings.Contains(seg.text, "plan") {
		t.Fatalf("segment text = %q, want to contain phase info", seg.text)
	}
}

func TestPhaseSeparatorHasBlankLinesAboveAndBelow(t *testing.T) {
	b := newTestBuffer(t)
	// Create a phase separator
	seg := contentSegment{
		kind: segmentSeparator,
		separatorData: &separatorData{label: "Phase: implement"},
		renderDirty: true,
	}

	out := b.renderSeparatorSegment(seg, 80)

	lines := strings.Split(out, "\n")
	// Expected: blank line, separator line, blank line
	// When split by \n, we get 4 elements (including trailing empty after final newline)
	if len(lines) < 3 {
		t.Errorf("phase separator output has too few lines: %q (expected blank line, separator, blank line)", out)
	}
	// First character should be newline, meaning blank line before
	if !strings.HasPrefix(out, "\n") {
		t.Errorf("phase separator should start with blank line: %q", out)
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
	b := newTestBuffer(t)
	// Create a non-phase separator (e.g., for compaction)
	seg := contentSegment{
		kind: segmentSeparator,
		separatorData: &separatorData{label: "Compaction", closing: false},
		renderDirty: true,
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

// mockPhaseTransitionEvent creates a mock phase transition event for testing
func mockPhaseTransitionEvent() interface{} {
	return map[string]interface{}{
		"from":       "plan",
		"to":         "implement",
		"status":     "starting",
		"model":      "test-model",
		"session_id": "test-session",
		"run_id":     "test-run",
	}
}

// mockFormatEvent simulates the FormatEvent function output
func mockFormatEvent(event interface{}) string {
	return "phase: phase transition plan -> implement starting model=test-model session=test-session run=test-run"
}
