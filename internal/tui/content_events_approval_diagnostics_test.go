package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// TestApprovalCorrelatesByCallIDAcrossConcurrentCalls reproduces the bug where
// two concurrent calls to the same tool (e.g. two parallel bash calls) could
// have their approval state misattributed because correlation matched on tool
// name alone. Each call is tracked as an independent segmentToolCall entry
// (not merged into a group) so the segments are kept apart by an unrelated
// segment, mirroring how two non-adjacent tool calls would appear in the
// transcript.
func TestApprovalCorrelatesByCallIDAcrossConcurrentCalls(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{
		collapseState: make(map[int]bool),
	}

	firstCall := &toolCallSegment{tool: "bash", args: "sleep 5", callID: "call-1", collapsed: true}
	secondCall := &toolCallSegment{tool: "bash", args: "sleep 10", callID: "call-2", collapsed: true}

	b.segments = append(b.segments,
		contentSegment{kind: segmentToolCall, toolData: firstCall},
		contentSegment{kind: segmentPlain}, // unrelated segment keeps the two calls apart
		contentSegment{kind: segmentToolCall, toolData: secondCall},
	)

	// Approval requested for the first call only.
	b.appendApprovalRequestedEvent(output.Event{
		Type: output.EventTypeApprovalRequested,
		Payload: output.ApprovalEvent{
			Tool:    "bash",
			CallID:  "call-1",
			Mode:    "prompt",
			Preview: `{"command":"sleep 5"}`,
		},
	})

	if !firstCall.approvalPending {
		t.Fatal("first call should have approvalPending = true")
	}
	if secondCall.approvalPending {
		t.Fatal("second call should not have approvalPending yet")
	}

	// Approval requested for the second call.
	b.appendApprovalRequestedEvent(output.Event{
		Type: output.EventTypeApprovalRequested,
		Payload: output.ApprovalEvent{
			Tool:    "bash",
			CallID:  "call-2",
			Mode:    "prompt",
			Preview: `{"command":"sleep 10"}`,
		},
	})

	if !secondCall.approvalPending {
		t.Fatal("second call should have approvalPending = true")
	}
	// The first call's approval state must not have been overwritten by the
	// second request.
	if !firstCall.approvalPending {
		t.Fatal("first call should still have approvalPending = true")
	}
	if firstCall.approvalMode != "prompt" || firstCall.approvalPreview != `{"command":"sleep 5"}` {
		t.Fatalf("first call approval state corrupted: mode=%q preview=%q", firstCall.approvalMode, firstCall.approvalPreview)
	}
	if secondCall.approvalMode != "prompt" || secondCall.approvalPreview != `{"command":"sleep 10"}` {
		t.Fatalf("second call approval state incorrect: mode=%q preview=%q", secondCall.approvalMode, secondCall.approvalPreview)
	}

	// Resolve the second call's approval; the first must remain untouched.
	b.appendApprovalDecisionEvent(output.Event{
		Type: output.EventTypeApprovalAccepted,
		Payload: output.ApprovalEvent{
			Tool:    "bash",
			CallID:  "call-2",
			Allowed: true,
		},
	})

	if !secondCall.approvalResolved || !secondCall.approvalAccepted {
		t.Fatal("second call should be resolved and accepted")
	}
	if firstCall.approvalResolved {
		t.Fatal("first call should not be resolved by the second call's decision")
	}
	if !firstCall.approvalPending {
		t.Fatal("first call should still be pending after second call's decision")
	}

	// Now resolve the first call's approval.
	b.appendApprovalDecisionEvent(output.Event{
		Type: output.EventTypeApprovalDenied,
		Payload: output.ApprovalEvent{
			Tool:    "bash",
			CallID:  "call-1",
			Allowed: false,
		},
	})

	if !firstCall.approvalResolved {
		t.Fatal("first call should be resolved")
	}
	if firstCall.approvalAccepted {
		t.Fatal("first call should have been denied, not accepted")
	}
}

func TestApprovalRequestedRetainsCallIDInToolCallAndFallbackPill(t *testing.T) {
	b := &contentBuffer{collapseState: make(map[int]bool)}
	call := &toolCallSegment{tool: "bash", callID: "call-normal"}
	b.segments = []contentSegment{{kind: segmentToolCall, toolData: call}}
	b.appendApprovalRequestedEvent(output.Event{Payload: output.ApprovalEvent{Tool: "bash", CallID: "call-normal"}})
	if call.approvalIdentity != "call-normal" {
		t.Fatalf("tool call identity = %q, want call-normal", call.approvalIdentity)
	}

	b.segments = nil
	b.appendApprovalRequestedEvent(output.Event{Payload: output.ApprovalEvent{Tool: "bash", CallID: "call-fallback"}})
	if got := b.segments[0].approvalData.identity; got != "call-fallback" {
		t.Fatalf("fallback identity = %q, want call-fallback", got)
	}
}

func TestApprovalFallbackDecisionCorrelatesByCallID(t *testing.T) {
	b := &contentBuffer{collapseState: make(map[int]bool)}
	for _, callID := range []string{"call-A", "call-B"} {
		b.appendApprovalRequestedEvent(output.Event{Payload: output.ApprovalEvent{
			Tool:   "bash",
			CallID: callID,
		}})
	}

	b.appendApprovalDecisionEvent(output.Event{
		Type:    output.EventTypeApprovalAccepted,
		Payload: output.ApprovalEvent{CallID: "call-B"},
	})

	first := b.segments[0].approvalData
	second := b.segments[1].approvalData
	if first.resolved {
		t.Fatal("call-A fallback pill should remain pending")
	}
	if second == nil || !second.resolved || !second.accepted {
		t.Fatal("call-B fallback pill should be resolved and accepted")
	}
	if first.queueDepth != 0 {
		t.Fatalf("call-A queue depth = %d, want 0 after call-B resolves", first.queueDepth)
	}
}

func TestApprovalQueueDepthsAreHeadRelativeAndRecomputed(t *testing.T) {
	b := &contentBuffer{styles: testStyles(theme.AccentAmber), collapseState: make(map[int]bool)}
	head := &approvalPillData{tool: "bash", agentID: "worker", queueDepth: 9}
	tail := &approvalPillData{tool: "read", queueDepth: 9}
	b.segments = []contentSegment{
		{kind: segmentApprovalPill, approvalData: head},
		{kind: segmentApprovalPill, approvalData: tail},
	}
	b.recomputeApprovalQueueDepths()
	if head.queueDepth != 1 || tail.queueDepth != 0 {
		t.Fatalf("depths = %d/%d, want 1/0", head.queueDepth, tail.queueDepth)
	}
	if rendered := stripANSI(b.renderApprovalPill(head, 100)); !strings.Contains(rendered, "+1 waiting") {
		t.Errorf("head render = %q, want +1 waiting", rendered)
	}
	if rendered := stripANSI(b.renderApprovalPill(tail, 100)); strings.Contains(rendered, "waiting") {
		t.Errorf("queued tail render = %q, want no waiting suffix", rendered)
	}
	head.resolved = true
	b.recomputeApprovalQueueDepths()
	if tail.queueDepth != 0 {
		t.Fatalf("tail depth after resolution = %d, want 0", tail.queueDepth)
	}
	if rendered := stripANSI(b.renderApprovalPill(tail, 100)); strings.Contains(rendered, "waiting") {
		t.Errorf("promoted sole head render = %q, want no waiting suffix", rendered)
	}
}

func TestApprovalPillAgentLabel(t *testing.T) {
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	for _, test := range []struct {
		name  string
		agent string
		want  string
	}{
		{name: "delegated", agent: "worker-1", want: "[agent: worker-1]"},
		{name: "parent", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			rendered := stripANSI(b.renderApprovalPill(&approvalPillData{tool: "bash", agentID: test.agent}, 100))
			if strings.Contains(rendered, "[agent:") != (test.want != "") {
				t.Errorf("render = %q, agent suffix presence = %v, want %v", rendered, strings.Contains(rendered, "[agent:"), test.want != "")
			}
			if test.want != "" && !strings.Contains(rendered, test.want) {
				t.Errorf("render = %q, want %q", rendered, test.want)
			}
		})
	}
}

func TestCompactionBannerElapsedMeasuresFromFirstEventEvenWithInterleaved(t *testing.T) {
	// Not t.Parallel(): this test overrides the package-level nanoNow var,
	// which races with any other test reading it concurrently (nanoNow is
	// shared package state, not per-instance) - matches the existing
	// convention of every other nanoNow-overriding test in this package.
	b := &contentBuffer{
		collapseState: make(map[int]bool),
	}

	// Override nanoNow to control time in the test
	savedNanoNow := nanoNow
	defer func() { nanoNow = savedNanoNow }()

	times := []int64{1000, 3000, 5000}
	timeIdx := 0
	nanoNow = func() int64 {
		defer func() { timeIdx++ }()
		if timeIdx < len(times) {
			return times[timeIdx]
		}
		return times[len(times)-1]
	}

	// First compacting event at time=1000
	timeIdx = 0
	b.handleCompactionDiagnostics(output.ContextCompactionEvent{
		Severity:        "compacting",
		CompactionCount: 1,
	})

	if len(b.segments) != 1 {
		t.Fatalf("after first compacting: segments len = %d, want 1", len(b.segments))
	}
	seg1 := b.segments[0]
	if seg1.kind != segmentCompactionBanner || seg1.compactionData == nil || seg1.compactionData.finished {
		t.Fatal("first segment should be unfinished compaction banner")
	}
	firstStartTime := seg1.compactionData.startTime

	// Interleave an unrelated segment (simulating delegation or other content)
	b.segments = append(b.segments, contentSegment{
		kind: segmentPlain,
	})

	if len(b.segments) != 2 {
		t.Fatalf("after interleaved: segments len = %d, want 2", len(b.segments))
	}

	// Second compacting event at time=3000
	timeIdx = 1
	b.handleCompactionDiagnostics(output.ContextCompactionEvent{
		Severity:        "compacting",
		CompactionCount: 2,
	})

	// Should still have 2 segments (the plain one didn't get removed)
	// The original banner should still exist and should have been updated with the new count
	foundUpdatedBanner := false
	for i, seg := range b.segments {
		if seg.kind == segmentCompactionBanner && seg.compactionData != nil && !seg.compactionData.finished {
			if i != 0 {
				t.Fatalf("found unfinished banner at index %d, should be at 0", i)
			}
			// The banner should have been updated with the new compaction count
			if seg.compactionData.compactionCount != 2 {
				t.Errorf("after second event, compactionCount = %d, want 2", seg.compactionData.compactionCount)
			}
			// Crucially, startTime should still be from the FIRST event, not the second
			if seg.compactionData.startTime != firstStartTime {
				t.Errorf("startTime changed: got %d, want %d (from first event)", seg.compactionData.startTime, firstStartTime)
			}
			foundUpdatedBanner = true
			break
		}
	}
	if !foundUpdatedBanner {
		t.Fatal("unfinished compaction banner not found after second event")
	}

	// Completion event at time=5000
	timeIdx = 2
	b.handleCompactionDiagnostics(output.ContextCompactionEvent{
		Severity:           "compact",
		CompactedMessages:  10,
		CompactionCount:    2,
		CompactedTurns:     1,
		RetainedTurns:      5,
		RetainedMessages:   50,
		BeforePromptTokens: 1000,
		AfterPromptTokens:  500,
		BeforeUsagePercent: 80,
		AfterUsagePercent:  40,
	})

	// Should still have 2 segments; the plain segment stays
	if len(b.segments) != 2 {
		t.Fatalf("after completion: segments len = %d, want 2", len(b.segments))
	}

	// First segment should be the completed banner
	completedBanner := b.segments[0]
	if completedBanner.kind != segmentCompactionBanner || completedBanner.compactionData == nil {
		t.Fatal("first segment should be completed banner")
	}
	if !completedBanner.compactionData.finished {
		t.Fatal("banner should be finished")
	}
	if completedBanner.compactionData.startTime != firstStartTime {
		t.Errorf("completed banner startTime = %d, want %d", completedBanner.compactionData.startTime, firstStartTime)
	}
	// Elapsed should be measured from first event (1000) to completion (5000) = 4000 nanos
	// which is much less than a second, so it should render as "0ms" or similar
	if completedBanner.compactionData.elapsed == "" {
		t.Error("elapsed should not be empty for banner with nonzero startTime")
	}

	// HasActiveCompactions should return false
	if b.HasActiveCompactions() {
		t.Error("HasActiveCompactions should be false after completion")
	}
}

func TestClearApprovalStateMarksSegmentsRenderDirty(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{
		collapseState: make(map[int]bool),
	}

	// Create tool call segments with approval state
	toolCall := &toolCallSegment{
		tool:             "bash",
		callID:           "call-1",
		approvalPending:  true,
		approvalResolved: true,
	}
	toolCallGroup := &toolCallGroupSegment{
		entries: []*toolCallSegment{
			{
				tool:             "read",
				callID:           "call-2",
				approvalPending:  true,
				approvalResolved: false,
			},
		},
	}

	b.segments = []contentSegment{
		{
			kind:        segmentToolCall,
			toolData:    toolCall,
			renderDirty: false,
		},
		{
			kind:          segmentToolCallGroup,
			toolGroupData: toolCallGroup,
			renderDirty:   false,
		},
	}

	// Clear approval state
	b.clearApprovalState()

	// Both segments should have been marked renderDirty
	if !b.segments[0].renderDirty {
		t.Error("tool call segment should be marked renderDirty after clearApprovalState")
	}
	if !b.segments[1].renderDirty {
		t.Error("tool call group segment should be marked renderDirty after clearApprovalState")
	}

	// Approval states should be cleared
	if toolCall.approvalPending {
		t.Error("toolCall.approvalPending should be false")
	}
	if toolCall.approvalResolved {
		t.Error("toolCall.approvalResolved should be false")
	}
	if toolCallGroup.entries[0].approvalPending {
		t.Error("toolCallGroup entry approvalPending should be false")
	}
	if toolCallGroup.entries[0].approvalResolved {
		t.Error("toolCallGroup entry approvalResolved should be false")
	}
}
