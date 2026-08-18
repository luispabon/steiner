package tui

import (
	"errors"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestHandleDelegationCompleteSetsCacheHitRateFromPayload(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:          make([]contentSegment, 0),
		collapseState:     make(map[int]bool),
		styles:            testStyles(theme.AccentAmber),
		activeDelegations: make(map[string]delegationLocator),
	}

	buffer.AppendEvent(output.Event{
		Type: output.EventTypeDelegationStarted,
		Payload: output.DelegationStartedEvent{
			AgentID:     "child-cache",
			TaskPreview: "task",
		},
	})
	buffer.AppendEvent(output.Event{
		Type: output.EventTypeDelegationComplete,
		Payload: output.DelegationCompleteEvent{
			AgentID:           "child-cache",
			Status:            "complete",
			TurnCount:         2,
			TokenCount:        1000,
			ToolCallCount:     3,
			InputTokens:       50,
			CacheReadTokens:   950,
			CacheCreateTokens: 0,
			Output:            "done",
		},
	})

	var dd *delegationDisplayState
	for _, seg := range buffer.segments {
		if seg.kind == segmentDelegation && seg.delegData != nil && seg.delegData.agentID == "child-cache" {
			dd = seg.delegData
			break
		}
	}
	if dd == nil {
		t.Fatalf("delegation segment not found")
	}
	if !dd.cacheHitOK {
		t.Fatalf("cacheHitOK = false, want true")
	}
	wantRate := 950.0 / 1000.0
	if diff := dd.cacheHitRate - wantRate; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("cacheHitRate = %v, want %v", dd.cacheHitRate, wantRate)
	}
}

func TestScopedDelegationCompactionStaysInsideDelegationSegment(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
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
		styles:        testStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "inspect docs"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "child-1",
		Status:        "complete",
		TurnCount:     1,
		TokenCount:    10,
		ToolCallCount: 0,
		Output:        "done",
	}))

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

func TestRemoveFromPendingDelegateParents(t *testing.T) {
	t.Parallel()
	dd3 := &delegationDisplayState{}
	dd7 := &delegationDisplayState{}
	dd10 := &delegationDisplayState{}
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: []delegationLocator{{seg: 3, dd: dd3}, {seg: 7, dd: dd7}, {seg: 10, dd: dd10}},
	}
	// Remove an existing element
	buffer.removeFromPendingDelegateParents(dd7)
	if len(buffer.pendingDelegateParents) != 2 {
		t.Fatalf("len = %d, want 2", len(buffer.pendingDelegateParents))
	}
	if buffer.pendingDelegateParents[0].dd != dd3 || buffer.pendingDelegateParents[1].dd != dd10 {
		t.Fatalf("got unexpected locators after remove")
	}
	// Remove a non-existent element — no-op
	otherDD := &delegationDisplayState{}
	buffer.removeFromPendingDelegateParents(otherDD)
	if len(buffer.pendingDelegateParents) != 2 {
		t.Fatalf("len = %d, want 2 after no-op", len(buffer.pendingDelegateParents))
	}
	// Remove last element
	buffer.removeFromPendingDelegateParents(dd10)
	if len(buffer.pendingDelegateParents) != 1 || buffer.pendingDelegateParents[0].dd != dd3 {
		t.Fatalf("got unexpected locators, want [dd3]")
	}
	// Remove first/only element
	buffer.removeFromPendingDelegateParents(dd3)
	if len(buffer.pendingDelegateParents) != 0 {
		t.Fatalf("len = %d, want 0", len(buffer.pendingDelegateParents))
	}
}

func TestDelegationToolCallFinished_DrainsQueue(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}
	// Simulate a delegate tool call that will fail before spawning
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "do stuff"}))

	if len(buffer.pendingDelegateParents) != 1 {
		t.Fatalf("pendingDelegateParents len = %d, want 1 after ToolCallStarted", len(buffer.pendingDelegateParents))
	}
	if len(buffer.segments) != 1 {
		t.Fatalf("segments len = %d, want 1", len(buffer.segments))
	}
	seg := buffer.segments[0]
	if seg.kind != segmentDelegation || seg.delegData == nil {
		t.Fatal("expected segmentDelegation with delegData")
	}
	if seg.delegData.agentID != "" {
		t.Fatalf("agentID = %q, want empty", seg.delegData.agentID)
	}

	// Fire ToolCallFinished with an error — should drain queue and mark failed
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "code", "call_1", "", errors.New("plan mode rejected")))

	if len(buffer.pendingDelegateParents) != 0 {
		t.Fatalf("pendingDelegateParents len = %d, want 0 after failed ToolCallFinished", len(buffer.pendingDelegateParents))
	}
	if seg.delegData.status != "failed" {
		t.Fatalf("status = %q, want \"failed\"", seg.delegData.status)
	}
	if !seg.renderDirty {
		t.Error("segment should be renderDirty after failure")
	}
}

func TestDelegationToolCallFinished_IgnoresSpawned(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}
	// Start the delegate tool
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "do stuff"}))
	// Delegation spawns
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "do stuff"))
	// Tool finishes (could be with or without error — either way, should not touch the segment)
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "code", "call_1", "some result", nil))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments len = %d, want 1", len(buffer.segments))
	}
	seg := buffer.segments[0]
	if seg.delegData == nil {
		t.Fatal("delegData is nil")
	}
	// Status should remain "active" (set by DelegationStarted), NOT "failed"
	if seg.delegData.status != "active" {
		t.Fatalf("status = %q, want \"active\" (ToolCallFinished should not modify spawned delegation)", seg.delegData.status)
	}
	// Queue should be drained by DelegationStarted, but ToolCallFinished should not touch it further
	if len(buffer.pendingDelegateParents) != 0 {
		t.Fatalf("pendingDelegateParents len = %d, want 0 (drained by DelegationStarted)", len(buffer.pendingDelegateParents))
	}
	// agentID should be set by DelegationStarted
	if seg.delegData.agentID != "child-1" {
		t.Fatalf("agentID = %q, want \"child-1\"", seg.delegData.agentID)
	}
}

func TestDelegationToolCallFinished_FollowUp_DrainsQueue(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}
	// Simulate a follow_up tool call that will fail before spawning
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "follow_up", "call_fu_1", map[string]any{"agent_id": "child-1", "message": "continue work"}))

	if len(buffer.pendingDelegateParents) != 1 {
		t.Fatalf("pendingDelegateParents len = %d, want 1 after ToolCallStarted", len(buffer.pendingDelegateParents))
	}
	if len(buffer.segments) != 1 {
		t.Fatalf("segments len = %d, want 1", len(buffer.segments))
	}
	seg := buffer.segments[0]
	if seg.kind != segmentDelegation || seg.delegData == nil {
		t.Fatal("expected segmentDelegation with delegData")
	}
	if seg.delegData.agentID != "" {
		t.Fatalf("agentID = %q, want empty", seg.delegData.agentID)
	}

	// Fire ToolCallFinished with an error — should drain queue and mark failed
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "follow_up", "call_fu_1", "", errors.New("plan mode rejected")))

	if len(buffer.pendingDelegateParents) != 0 {
		t.Fatalf("pendingDelegateParents len = %d, want 0 after failed ToolCallFinished", len(buffer.pendingDelegateParents))
	}
	if seg.delegData.status != "failed" {
		t.Fatalf("status = %q, want \"failed\"", seg.delegData.status)
	}
	if !seg.renderDirty {
		t.Error("segment should be renderDirty after failure")
	}
}

func TestAdvisorThinkingChunkRoutingBySource(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		showThinking:  true,
	}

	// Start advisor call.
	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 1, "", nil))
	if len(buffer.segments) != 1 {
		t.Fatalf("segments after start = %d, want 1", len(buffer.segments))
	}

	// Emit assistant-sourced thinking chunk while advisor box is active.
	// This should NOT route to the advisor box but to a normal thinking segment.
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(0, "assistant thinking", output.ChunkSourceAssistant))

	// Verify advisor box has no entries (hasn't received the assistant thinking).
	advisorSeg := buffer.segments[0]
	if advisorSeg.delegData == nil || len(advisorSeg.delegData.entries) != 0 {
		t.Fatalf("advisor entries = %d, want 0 (assistant chunk should not route to advisor)", len(advisorSeg.delegData.entries))
	}

	// Verify a new thinking segment was created for the assistant chunk.
	if len(buffer.segments) != 2 {
		t.Fatalf("segments count = %d, want 2 (advisor box + thinking segment)", len(buffer.segments))
	}
	if buffer.segments[1].kind != segmentThinkingBlock {
		t.Fatalf("segment[1].kind = %v, want segmentThinkingBlock", buffer.segments[1].kind)
	}

	// Now emit advisor-sourced thinking chunk.
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(0, "advisor thinking", output.ChunkSourceAdvisor))

	// Verify advisor box now has the thinking entry.
	if len(advisorSeg.delegData.entries) != 1 {
		t.Fatalf("advisor entries = %d, want 1 (advisor chunk should route to advisor)", len(advisorSeg.delegData.entries))
	}

	// Verify segment count hasn't increased (advisor chunk merged into advisor box).
	if len(buffer.segments) != 2 {
		t.Fatalf("segments count = %d, want 2 (advisor chunk should not create new segment)", len(buffer.segments))
	}
}

func TestAdvisorThinkingChunkStripsMarkersAndMerges(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
		showThinking:  true,
	}

	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 1, "", nil))
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "**step one**", output.ChunkSourceAdvisor))
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "\n**step two**", output.ChunkSourceAdvisor))

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want advisor delegation state")
	}
	if got := len(dd.entries); got != 1 {
		t.Fatalf("entries count = %d, want 1 (merged thinking)", got)
	}
	if got := dd.entries[0].body; got != "**step one**\n**step two**" {
		t.Fatalf("entry body = %q, want %q", got, "**step one**\n**step two**")
	}

	rows := buffer.renderDelegationThinkingEntry(dd.entries[0], 80)
	var rendered strings.Builder
	for _, row := range rows {
		rendered.WriteString(stripANSI(row))
		rendered.WriteString("\n")
	}
	plain := rendered.String()
	if strings.Contains(plain, "**") {
		t.Fatalf("render contains markdown bold markers: %q", plain)
	}
	for _, want := range []string{"step one", "step two"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("render = %q, want %q", plain, want)
		}
	}
}
