package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestScopedDelegationCompactionStaysInsideDelegationSegment(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "inspect docs"))
	buffer.AppendEvent(output.WithAgentScope(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:               "compaction",
		Severity:           "compacting",
		CompactionCount:    1,
		BeforePromptTokens: 2000,
	}), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:            "compaction",
		Severity:        "info",
		CompactionCount: 1,
		SummaryTitle:    "compacted child history",
		SummaryText:     "child-only summary",
	}), "child-1"))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments = %d, want only delegation segment", len(buffer.segments))
	}
	seg := buffer.segments[0]
	if seg.kind != segmentDelegation {
		t.Fatalf("segment kind = %v, want segmentDelegation", seg.kind)
	}
	if seg.compactionData != nil {
		t.Fatal("scoped child compaction created top-level compaction banner")
	}
	if seg.delegData == nil {
		t.Fatal("delegData = nil")
	}
	if got, want := seg.delegData.currentOperation, "compacted child history"; got != want {
		t.Fatalf("currentOperation = %q, want %q", got, want)
	}

	rendered := buffer.String(80)
	if strings.Contains(rendered, "child-only summary") {
		t.Fatalf("rendered output leaked child compaction summary: %q", rendered)
	}
	if !strings.Contains(rendered, "compacted child history") {
		t.Fatalf("rendered output missing child-local compaction status: %q", rendered)
	}
}

func TestRenderDelegationSegmentKeepsBoxWidthBounded(t *testing.T) {
	useTrueColor(t)
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "inspect docs"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent("child-1", "complete", 1, 10, 0, "done"))

	segment := buffer.segments[0]
	rendered := strings.TrimSuffix(buffer.renderDelegationSegment(segment, 50), "\n")
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		if i < len(lines)-1 && lipgloss.Width(line) != 50 {
			t.Fatalf("box line width = %d, want 50 for line %q", lipgloss.Width(line), stripANSI(line))
		}
		if lipgloss.Width(line) > 50 {
			t.Fatalf("line width = %d, want <= 50 for line %q", lipgloss.Width(line), stripANSI(line))
		}
	}
}
