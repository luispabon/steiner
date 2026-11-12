package tui

import (
	"reflect"
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
			Tool:      "bash",
			CallID:    "call-1",
			RequestID: "call-1",
			Mode:      "prompt",
			Preview:   `{"command":"sleep 5"}`,
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
			Tool:      "bash",
			CallID:    "call-2",
			RequestID: "call-2",
			Mode:      "prompt",
			Preview:   `{"command":"sleep 10"}`,
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
			Tool:      "bash",
			CallID:    "call-2",
			RequestID: "call-2",
			Allowed:   true,
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
			Tool:      "bash",
			CallID:    "call-1",
			RequestID: "call-1",
			Allowed:   false,
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
	t.Parallel()
	b := &contentBuffer{collapseState: make(map[int]bool)}
	call := &toolCallSegment{tool: "bash", callID: "call-normal"}
	b.segments = []contentSegment{{kind: segmentToolCall, toolData: call}}
	b.appendApprovalRequestedEvent(output.Event{Payload: output.ApprovalEvent{Tool: "bash", CallID: "call-normal", RequestID: "approval-normal"}})
	if got, want := call.approvalIdentity, "approval-normal"; got != want {
		t.Fatalf("tool call identity = %q, want %q", got, want)
	}

	b.segments = nil
	b.appendApprovalRequestedEvent(output.Event{Payload: output.ApprovalEvent{Tool: "bash", CallID: "call-fallback", RequestID: "approval-fallback"}})
	if got, want := b.segments[0].approvalData.identity, "approval-fallback"; got != want {
		t.Fatalf("fallback identity = %q, want %q", got, want)
	}
}

func TestApprovalFallbackDecisionCorrelatesByCallID(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{collapseState: make(map[int]bool)}
	for _, callID := range []string{"call-A", "call-B"} {
		b.appendApprovalRequestedEvent(output.Event{Payload: output.ApprovalEvent{
			Tool:      "bash",
			CallID:    callID,
			RequestID: callID,
		}})
	}

	b.appendApprovalDecisionEvent(output.Event{
		Type:    output.EventTypeApprovalAccepted,
		Payload: output.ApprovalEvent{CallID: "call-B", RequestID: "call-B"},
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
	t.Parallel()
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
	t.Parallel()
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

func TestCompactionBannerFallbackFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		payload    output.ContextCompactionEvent
		wantModel  string
		wantAlias  string
		wantStatus string
		wantReason string
		wantHeader string
	}{
		{
			name: "used with alias",
			payload: output.ContextCompactionEvent{
				Severity:             "ok",
				SummaryTitle:         "ctx",
				CompactionCount:      1,
				CompactionModel:      "anthropic/claude-haiku-5-5",
				CompactionModelAlias: "haiku",
				FallbackStatus:       output.CompactionFallbackUsed,
				FallbackReason:       "primary over budget",
			},
			wantModel:  "anthropic/claude-haiku-5-5",
			wantAlias:  "haiku",
			wantStatus: output.CompactionFallbackUsed,
			wantReason: "primary over budget",
			wantHeader: "haiku",
		},
		{
			name: "used without alias",
			payload: output.ContextCompactionEvent{
				Severity:        "ok",
				SummaryTitle:    "ctx",
				CompactionCount: 1,
				CompactionModel: "claude-haiku-5-5",
				FallbackStatus:  output.CompactionFallbackUsed,
				FallbackReason:  "cache expired",
			},
			wantModel:  "claude-haiku-5-5",
			wantStatus: output.CompactionFallbackUsed,
			wantReason: "cache expired",
			wantHeader: "claude-haiku-5-5",
		},
		{
			name: "skipped",
			payload: output.ContextCompactionEvent{
				Severity:        "ok",
				SummaryTitle:    "ctx",
				CompactionCount: 1,
				CompactionModel: "claude-haiku-5-5",
				FallbackStatus:  output.CompactionFallbackSkipped,
				FallbackReason:  "no fallback configured",
			},
			wantModel:  "claude-haiku-5-5",
			wantStatus: output.CompactionFallbackSkipped,
			wantReason: "no fallback configured",
			wantHeader: "fallback skipped",
		},
		{
			name: "skipped severity",
			payload: output.ContextCompactionEvent{
				Severity:        "skipped",
				CompactionCount: 1,
				FallbackStatus:  output.CompactionFallbackSkipped,
				FallbackReason:  "fallback failed: boom",
			},
			wantStatus: output.CompactionFallbackSkipped,
			wantReason: "fallback failed: boom",
			wantHeader: "fallback skipped",
		},
		{
			name: "absent",
			payload: output.ContextCompactionEvent{
				Severity:        "ok",
				SummaryTitle:    "ctx",
				CompactionCount: 1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newTestBuffer(t)
			b.handleCompactionDiagnostics(tt.payload)
			if len(b.segments) != 1 {
				t.Fatalf("segments = %d, want 1", len(b.segments))
			}
			seg := b.segments[0]
			if seg.kind != segmentCompactionBanner || seg.compactionData == nil {
				t.Fatalf("segment = %#v, want compaction banner with data", seg)
			}
			data := seg.compactionData
			if data.compactionModel != tt.wantModel {
				t.Errorf("compactionModel = %q, want %q", data.compactionModel, tt.wantModel)
			}
			if data.compactionModelAlias != tt.wantAlias {
				t.Errorf("compactionModelAlias = %q, want %q", data.compactionModelAlias, tt.wantAlias)
			}
			if data.fallbackStatus != tt.wantStatus {
				t.Errorf("fallbackStatus = %q, want %q", data.fallbackStatus, tt.wantStatus)
			}
			if data.fallbackReason != tt.wantReason {
				t.Errorf("fallbackReason = %q, want %q", data.fallbackReason, tt.wantReason)
			}
			header := stripANSI(b.compactionBoxRows(data, 120)[0])
			if !strings.Contains(header, tt.wantHeader) {
				t.Errorf("header = %q, want it to contain %q", header, tt.wantHeader)
			}
			if tt.wantReason != "" && strings.Contains(header, tt.wantReason) {
				t.Errorf("header = %q, want the fallback reason kept to the detail rows", header)
			}
		})
	}
}

// TestScopedSubAgentCompactionDoesNotAlterOrchestratorFallback checks that a
// compaction event scoped to a bound sub-agent updates only that delegation and
// leaves the orchestrator banner's fallback model untouched.
func TestScopedSubAgentCompactionDoesNotAlterOrchestratorFallback(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}
	b.handleCompactionDiagnostics(output.ContextCompactionEvent{
		Severity:        "ok",
		SummaryTitle:    "ctx",
		CompactionCount: 1,
		CompactionModel: "claude-haiku-5-5",
		FallbackStatus:  output.CompactionFallbackUsed,
		FallbackReason:  "primary over budget",
	})
	b.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-1"), "inspect docs", "", ""))
	b.AppendEvent(output.WithAgentScope(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:            "compaction",
		Severity:        "info",
		CompactionCount: 1,
		SummaryTitle:    "child summary",
		CompactionModel: "child-model",
		FallbackStatus:  output.CompactionFallbackSkipped,
		FallbackReason:  "child reason",
	}), "child-1"))

	banners := 0
	var model, status string
	for _, seg := range b.segments {
		if seg.kind == segmentCompactionBanner && seg.compactionData != nil {
			banners++
			model = seg.compactionData.compactionModel
			status = seg.compactionData.fallbackStatus
		}
	}
	if banners != 1 {
		t.Fatalf("compaction banners = %d, want 1", banners)
	}
	if want := "claude-haiku-5-5"; model != want {
		t.Errorf("orchestrator compactionModel = %q, want %q", model, want)
	}
	if want := output.CompactionFallbackUsed; status != want {
		t.Errorf("orchestrator fallbackStatus = %q, want %q", status, want)
	}
}

// TestUnboundScopedCompactionDoesNotAlterOrchestratorBanner covers compaction
// events scoped to an agent with no delegation binding. They must leave the
// orchestrator banner exactly as it was, and must not create one.
func TestUnboundScopedCompactionDoesNotAlterOrchestratorBanner(t *testing.T) {
	t.Parallel()
	events := []output.Event{
		output.WithAgentScope(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
			Kind:            "compaction",
			Severity:        "compacting",
			CompactionCount: 2,
		}), "ghost-child"),
		output.WithAgentScope(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
			Kind:            "compaction",
			Severity:        "info",
			CompactionCount: 2,
			SummaryTitle:    "child summary",
			CompactionModel: "child-model",
			FallbackStatus:  output.CompactionFallbackSkipped,
			FallbackReason:  "child reason",
		}), "ghost-child"),
	}
	tests := []struct {
		name  string
		prior *compactionBannerData
	}{
		{name: "no orchestrator banner"},
		{
			name: "finished banner with fallback model",
			prior: &compactionBannerData{
				label:                "Context compacted",
				subtitle:             "ctx",
				finished:             true,
				compactionCount:      1,
				compactionModel:      "anthropic/claude-haiku-5-5",
				compactionModelAlias: "haiku",
				fallbackStatus:       output.CompactionFallbackUsed,
				fallbackReason:       "primary over budget",
			},
		},
		{
			name: "in-progress banner",
			prior: &compactionBannerData{
				label:           "Compacting",
				subtitle:        "summarizing context",
				compactionCount: 1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &contentBuffer{
				segments:      make([]contentSegment, 0),
				collapseState: make(map[int]bool),
				styles:        testStyles(theme.AccentAmber),
			}
			var want compactionBannerData
			if tt.prior != nil {
				// Copy before routing so an in-place mutation of the banner is also caught.
				want = *tt.prior
				b.segments = append(b.segments, contentSegment{kind: segmentCompactionBanner, compactionData: tt.prior, renderDirty: true})
			}
			for _, ev := range events {
				b.AppendEvent(ev)
			}
			if tt.prior == nil {
				if len(b.segments) != 0 {
					t.Fatalf("segments = %d, want 0 (no banner created)", len(b.segments))
				}
				return
			}
			if len(b.segments) != 1 {
				t.Fatalf("segments = %d, want 1", len(b.segments))
			}
			seg := b.segments[0]
			if seg.kind != segmentCompactionBanner || seg.compactionData == nil {
				t.Fatalf("segment = %#v, want compaction banner with data", seg)
			}
			if !reflect.DeepEqual(*seg.compactionData, want) {
				t.Errorf("orchestrator banner changed: got %+v, want %+v", *seg.compactionData, want)
			}
		})
	}
}

// TestCompactionCompactingEventShowsFallbackModel checks the running banner
// names the fallback model in its one-row header as soon as the selection event
// arrives, before the summary call finishes.
func TestCompactionCompactingEventShowsFallbackModel(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	b.handleCompactionDiagnostics(output.ContextCompactionEvent{
		Severity:             "compacting",
		CompactionCount:      2,
		CompactionModel:      "anthropic/claude-haiku-5-5",
		CompactionModelAlias: "haiku",
		FallbackStatus:       output.CompactionFallbackUsed,
		FallbackReason:       "primary over budget",
	})

	if len(b.segments) != 1 {
		t.Fatalf("segments = %d, want 1", len(b.segments))
	}
	data := b.segments[0].compactionData
	if data == nil || data.finished {
		t.Fatalf("banner data = %+v, want an unfinished banner", data)
	}
	rows := b.compactionBoxRows(data, 120)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want one header row: %q", len(rows), rows)
	}
	header := stripANSI(rows[0])
	if !strings.Contains(header, "haiku") {
		t.Errorf("header = %q, want the fallback alias", header)
	}
	if strings.Contains(header, "primary over budget") {
		t.Errorf("header = %q, want the fallback reason kept to the detail rows", header)
	}
}

// TestCompactionActiveEventsDoNotDuplicateStatusFragment checks a second active
// event for the same run does not stack another compacting status fragment.
func TestCompactionActiveEventsDoNotDuplicateStatusFragment(t *testing.T) {
	t.Parallel()
	m := newModel(Config{Model: "test-model", ModelContexts: map[string]int{"test-model": 100000}}, nil)
	m.applyCompactionEvent(output.ContextCompactionEvent{Severity: "compacting", CompactionCount: 1})
	m.applyCompactionEvent(output.ContextCompactionEvent{
		Severity:             "compacting",
		CompactionCount:      1,
		CompactionModel:      "anthropic/claude-haiku-5-5",
		CompactionModelAlias: "haiku",
		FallbackStatus:       output.CompactionFallbackUsed,
		FallbackReason:       "primary over budget",
	})

	if got := strings.Count(m.status.context, "compacting"); got != 1 {
		t.Errorf("status context = %q, has %d compacting fragments, want 1", m.status.context, got)
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
	b.clearApprovalStateExcept(nil)

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

// Queue depth must distinguish a coordinated queue with no unclaimed head from
// a run with no coordinator at all. Only a coordinator's head carries the
// suffix, so a fully claimed queue carries none and transcript order never
// invents one.
func TestApprovalQueueDepthDistinguishesCoordinatedEmptyHead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		coordinated bool
		head        string
		wantFirst   int
		wantSecond  int
	}{
		{name: "no coordinator falls back to the first pending segment", wantFirst: 1, wantSecond: 0},
		{name: "coordinated empty head attaches no suffix", coordinated: true, wantFirst: 0, wantSecond: 0},
		{name: "coordinated head owns the suffix", coordinated: true, head: "approval-second", wantFirst: 0, wantSecond: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buffer := &contentBuffer{collapseState: make(map[int]bool), styles: testStyles("#e8814b")}
			buffer.appendApprovalRequestedEvent(output.NewApprovalRequestedEvent(output.ApprovalEventParams{Tool: "write", RequestID: "approval-first", Mode: "prompt", Kind: "path"}))
			buffer.appendApprovalRequestedEvent(output.NewApprovalRequestedEvent(output.ApprovalEventParams{Tool: "write", RequestID: "approval-second", Mode: "prompt", Kind: "path"}))

			buffer.approvalCoordinated = tc.coordinated
			buffer.approvalHead = tc.head
			buffer.recomputeApprovalQueueDepths()

			first := findApprovalPill(t, buffer, "approval-first")
			second := findApprovalPill(t, buffer, "approval-second")
			if first == nil || second == nil {
				t.Fatalf("pills = %#v/%#v, want both present", first, second)
			}
			if first.queueDepth != tc.wantFirst {
				t.Fatalf("first queue depth = %d, want %d", first.queueDepth, tc.wantFirst)
			}
			if second.queueDepth != tc.wantSecond {
				t.Fatalf("second queue depth = %d, want %d", second.queueDepth, tc.wantSecond)
			}
		})
	}
}
