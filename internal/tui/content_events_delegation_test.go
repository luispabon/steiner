package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestDelegationCacheWaitingBindsAndClears(t *testing.T) {
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}
	deadline := time.Now().Add(10 * time.Second)

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "inspect cache"}))
	buffer.AppendEvent(output.NewDelegationCacheWaitingEvent("child-1", "call_1", deadline))

	loc, ok := buffer.activeDelegations["child-1"]
	if !ok || loc.dd == nil {
		t.Fatal("active delegation child-1 not found")
	}
	if !loc.dd.cacheWaiting {
		t.Fatal("cacheWaiting = false, want true")
	}
	if loc.dd.cacheWaitDeadline != deadline.UnixNano() {
		t.Fatalf("cacheWaitDeadline = %d, want %d", loc.dd.cacheWaitDeadline, deadline.UnixNano())
	}

	buffer.AppendEvent(output.NewDelegationCacheWaitingEvent("unknown", "", deadline))
	if _, ok := buffer.activeDelegations["unknown"]; ok {
		t.Fatal("unknown delegation was added for empty CallID")
	}
	if !loc.dd.cacheWaiting || loc.dd.cacheWaitDeadline != deadline.UnixNano() {
		t.Fatal("empty-CallID event changed existing delegation state")
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "inspect cache", "call_1"))
	if loc.dd.cacheWaiting {
		t.Fatal("cacheWaiting = true after DelegationStarted, want false")
	}
}

func TestDelegationCacheWaitingProductionEventOrder(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call-production", map[string]any{"task": "wait for cache"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-production", "wait for cache", "call-production"))
	buffer.AppendEvent(output.NewDelegationCacheWaitingEvent("child-production", "call-production", time.Now().Add(time.Second)))

	loc, active := buffer.activeDelegations["child-production"]
	if !active || loc.dd == nil {
		t.Fatal("production-order delegation is not active")
	}
	if !loc.dd.cacheWaiting {
		t.Fatal("cacheWaiting = false, want true after late cache-waiting event")
	}
	if rows := buffer.ActiveDelegateRows(); len(rows) != 1 || rows[0].agentID != "child-production" {
		t.Fatalf("active rows = %#v, want child-production", rows)
	}

	buffer.AppendEvent(output.WithAgentScope(output.NewModelCallStartedEvent(1, "backend-model", 1), "child-production"))
	if loc.dd.cacheWaiting {
		t.Fatal("cacheWaiting = true after first scoped child event, want false")
	}
	if loc.dd.status != "active" {
		t.Fatalf("status = %q after first scoped child event, want active", loc.dd.status)
	}
}

func TestCacheWaitingCancellationLeavesElapsedEmpty(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "wait for cache"}))
	buffer.AppendEvent(output.NewDelegationCacheWaitingEvent("child-1", "call_1", time.Now().Add(time.Second)))

	loc := buffer.activeDelegations["child-1"]
	if loc.dd == nil {
		t.Fatal("cache-waiting delegation state is nil")
	}
	if loc.dd.startTime != 0 {
		t.Fatalf("cache-waiting startTime = %d, want 0", loc.dd.startTime)
	}
	buffer.AppendEvent(output.NewStopReasonEvent(1, "cancelled", nil))

	if loc.dd.status != "failed" {
		t.Fatalf("status = %q, want failed", loc.dd.status)
	}
	if loc.dd.elapsed != "" {
		t.Fatalf("elapsed = %q, want empty for cache-waiting cancellation", loc.dd.elapsed)
	}
}

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

func TestScopedDelegationEvents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		event       output.Event
		wantTPS     float64
		wantHandled bool
		wantNoState bool
	}{
		{
			name: "model call finished updates output tps",
			event: output.WithAgentScope(output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{
				Turn:      1,
				OutputTPS: 42.1,
			}), "child-1"),
			wantTPS:     42.1,
			wantHandled: true,
		},
		{
			name: "model call finished with wrong payload",
			event: output.WithAgentScope(output.Event{
				Type:    output.EventTypeModelCallFinished,
				Payload: output.APIResponseEvent{},
			}, "child-1"),
			wantHandled: true,
		},
		{
			name:        "api response is swallowed",
			event:       output.WithAgentScope(output.NewAPIResponseEvent(nil, nil, "stop", nil), "child-1"),
			wantHandled: true,
			wantNoState: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buffer := &contentBuffer{
				segments:          make([]contentSegment, 0),
				collapseState:     make(map[int]bool),
				styles:            testStyles(theme.AccentAmber),
				activeDelegations: make(map[string]delegationLocator),
			}
			buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "inspect docs"))

			loc, ok := buffer.activeDelegations["child-1"]
			if !ok || loc.dd == nil {
				t.Fatal("active delegation child-1 not found")
			}
			before := *loc.dd
			handled := buffer.appendScopedDelegationEvent(tc.event)
			if handled != tc.wantHandled {
				t.Fatalf("handled = %v, want %v", handled, tc.wantHandled)
			}
			if loc.dd.outputTPS != tc.wantTPS {
				t.Fatalf("outputTPS = %v, want %v", loc.dd.outputTPS, tc.wantTPS)
			}
			if tc.wantNoState && !reflect.DeepEqual(*loc.dd, before) {
				t.Fatalf("delegation state changed: before=%#v after=%#v", before, *loc.dd)
			}
		})
	}
}

func TestParentCancellationFinalizesAllActiveDelegations(t *testing.T) {
	originalNanoNow := nanoNow
	now := int64(10_000_000_000)
	nanoNow = func() int64 { return now }
	defer func() { nanoNow = originalNanoNow }()

	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first task"))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "second task"))
	buffer.String(80)
	if buffer.segments[0].renderDirty {
		t.Fatal("delegation group remains dirty after initial render")
	}

	now = 13_200_000_000
	buffer.AppendEvent(output.NewStopReasonEvent(1, "cancelled", nil))

	if buffer.HasActiveDelegations() {
		t.Fatal("HasActiveDelegations = true after parent cancellation, want false")
	}
	if len(buffer.segments) < 1 || buffer.segments[0].kind != segmentDelegationGroup {
		t.Fatalf("segments = %#v, want delegation group first", buffer.segments)
	}
	group := buffer.segments[0].delegGroupData
	if group == nil || len(group.entries) != 2 {
		t.Fatalf("group entries = %d, want 2", len(group.entries))
	}
	for _, dd := range group.entries {
		if dd.status != "failed" {
			t.Fatalf("delegation %q status = %q, want failed", dd.agentID, dd.status)
		}
		if !dd.finalizedByCancellation {
			t.Fatalf("delegation %q missing cancellation provenance", dd.agentID)
		}
		if dd.elapsed != "3s" {
			t.Fatalf("delegation %q elapsed = %q, want 3s", dd.agentID, dd.elapsed)
		}
	}
	if !buffer.segments[0].renderDirty {
		t.Fatal("delegation group segment not marked dirty")
	}

	now = 99_000_000_000
	for _, agentID := range []string{"child-1", "child-2"} {
		loc, found := buffer.findDelegation(agentID)
		if !found || loc.dd == nil {
			t.Fatalf("delegation %q not found after cancellation", agentID)
		}
		if loc.dd.elapsed != "3s" {
			t.Fatalf("delegation %q elapsed changed after cancellation: %q", agentID, loc.dd.elapsed)
		}
	}
	buffer.AppendEvent(output.NewStopReasonEvent(2, "cancelled", nil))
	for _, agentID := range []string{"child-1", "child-2"} {
		loc, found := buffer.findDelegation(agentID)
		if !found || loc.dd == nil || loc.dd.elapsed != "3s" {
			t.Fatalf("delegation %q was changed by duplicate cancellation", agentID)
		}
	}
}

func TestScopedCancellationFinalizesOnlyTargetDelegation(t *testing.T) {
	originalNanoNow := nanoNow
	now := int64(20_000_000_000)
	nanoNow = func() int64 { return now }
	defer func() { nanoNow = originalNanoNow }()

	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first task"))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "second task"))

	now = 24_500_000_000
	cancel := output.WithAgentScope(output.NewStopReasonEvent(1, "cancelled", nil), "child-1")
	buffer.AppendEvent(cancel)

	if _, active := buffer.activeDelegations["child-1"]; active {
		t.Fatal("child-1 remains active after scoped cancellation")
	}
	loc, active := buffer.activeDelegations["child-2"]
	if !active || loc.dd == nil {
		t.Fatal("child-2 not active after child-1 scoped cancellation")
	}
	if loc.dd.status != "active" {
		t.Fatalf("child-2 status = %q, want active", loc.dd.status)
	}
	child1, found := buffer.findDelegation("child-1")
	if !found || child1.dd == nil {
		t.Fatal("child-1 delegation not found after cancellation")
	}
	if child1.dd.status != "failed" {
		t.Fatalf("child-1 status = %q, want failed", child1.dd.status)
	}
	if !child1.dd.finalizedByCancellation {
		t.Fatal("child-1 missing cancellation provenance")
	}
	if child1.dd.elapsed != "4s" {
		t.Fatalf("child-1 elapsed = %q, want 4s", child1.dd.elapsed)
	}

	now = 90_000_000_000
	frozen := buffer.renderDelegationHeaderMeta(child1.dd)
	if !strings.Contains(stripANSI(frozen), "4s") {
		t.Fatalf("finalized child meta = %q, want frozen elapsed", stripANSI(frozen))
	}
	live := buffer.renderDelegationHeaderMeta(loc.dd)
	if strings.Contains(stripANSI(live), "4s") {
		t.Fatalf("sibling meta = %q, want live elapsed", stripANSI(live))
	}

	segmentsBeforeLateEvents := len(buffer.segments)
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID: "child-1",
		Status:  "complete",
	}))
	buffer.AppendEvent(output.NewDelegationFailedEvent("child-1", "first task", "late failure"))
	if len(buffer.segments) != segmentsBeforeLateEvents {
		t.Fatalf("segments after unscoped late terminal events = %d, want %d", len(buffer.segments), segmentsBeforeLateEvents)
	}
	if !buffer.HasActiveDelegations() {
		t.Fatal("late terminal event changed sibling active state")
	}
	if _, active := buffer.activeDelegations["child-1"]; active {
		t.Fatal("late terminal event restarted child-1")
	}
}

func TestUnknownDelegationTerminalEventsUseFallbackDisplay(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID: "unknown-complete",
		Status:  "complete",
	}))
	buffer.AppendEvent(output.NewDelegationFailedEvent("unknown-failed", "unknown task", "error"))

	states := delegationStates(buffer)
	if len(states) != 2 {
		t.Fatalf("delegation states = %d, want 2 fallback displays", len(states))
	}
	if states[0].agentID != "unknown-complete" || states[0].status != "complete" {
		t.Fatalf("complete fallback = %#v", states[0])
	}
	if states[1].agentID != "unknown-failed" || states[1].status != "failed" {
		t.Fatalf("failed fallback = %#v", states[1])
	}
}

func TestDelegationContextUsesRawPromptTokensForFill(t *testing.T) {
	b := newTestBuffer(t)
	b.AppendEvent(output.NewDelegationStartedEvent("child-raw", "inspect docs"))

	b.AppendEvent(output.WithAgentScope(output.NewContextTokenBudgetEvent(
		"conversation", 1, 100, 250, 1000, 10, 80, 0, 100, "ok", false,
	), "child-raw"))

	loc, ok := b.activeDelegations["child-raw"]
	if !ok || loc.dd == nil {
		t.Fatal("active delegation child-raw not found")
	}
	if loc.dd.promptTokens != 250 {
		t.Fatalf("promptTokens = %d, want 250", loc.dd.promptTokens)
	}
	if got := stripANSI(b.renderDelegationHeaderMeta(loc.dd)); !strings.Contains(got, "ctx: 25%") {
		t.Fatalf("delegation meta = %q, want raw occupancy", got)
	}
	if strings.Contains(stripANSI(b.renderDelegationHeaderMeta(loc.dd)), "ctx: 10%") {
		t.Fatalf("delegation meta = %q, used calibrated occupancy", stripANSI(b.renderDelegationHeaderMeta(loc.dd)))
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

func TestPendingDelegationDequeueHelpers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		set   func(*contentBuffer, []delegationLocator)
		byID  func(*contentBuffer, string) (delegationLocator, bool)
		drain func(*contentBuffer) (delegationLocator, bool)
		get   func(*contentBuffer) []delegationLocator
	}{
		{
			name: "delegate parent",
			set:  func(b *contentBuffer, locs []delegationLocator) { b.pendingDelegateParents = locs },
			byID: func(b *contentBuffer, callID string) (delegationLocator, bool) {
				return b.dequeuePendingDelegateParentByCallID(callID)
			},
			drain: func(b *contentBuffer) (delegationLocator, bool) {
				return b.dequeuePendingDelegateParentSegment()
			},
			get: func(b *contentBuffer) []delegationLocator { return b.pendingDelegateParents },
		},
		{
			name: "delegation start",
			set:  func(b *contentBuffer, locs []delegationLocator) { b.pendingDelegationStarts = locs },
			byID: func(b *contentBuffer, callID string) (delegationLocator, bool) {
				return b.dequeuePendingDelegationStartByCallID(callID)
			},
			drain: func(b *contentBuffer) (delegationLocator, bool) {
				return b.dequeuePendingDelegationStartSegment()
			},
			get: func(b *contentBuffer) []delegationLocator { return b.pendingDelegationStarts },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buffer := &contentBuffer{segments: make([]contentSegment, 1)}
			byID := &delegationDisplayState{parentCallID: "target"}
			other := &delegationDisplayState{parentCallID: "other", agentID: "child"}
			tc.set(buffer, []delegationLocator{
				{seg: 0, dd: nil},
				{seg: -1, dd: other},
				{seg: 0, dd: other},
				{seg: 0, dd: byID},
			})

			if _, ok := tc.byID(buffer, ""); ok {
				t.Fatal("empty call ID matched pending locator")
			}
			got, ok := tc.byID(buffer, "target")
			if !ok || got.dd != byID {
				t.Fatalf("by-call-ID result = %#v, %t, want target locator", got, ok)
			}
			if remaining := tc.get(buffer); len(remaining) != 3 {
				t.Fatalf("remaining after by-call-ID dequeue = %d, want 3", len(remaining))
			}

			tc.set(buffer, []delegationLocator{
				{seg: 0, dd: nil},
				{seg: -1, dd: other},
				{seg: 0, dd: other},
				{seg: 0, dd: &delegationDisplayState{}},
			})
			got, ok = tc.drain(buffer)
			if !ok || got.dd == nil {
				t.Fatalf("segment drain result = %#v, %t, want eligible locator", got, ok)
			}
			if remaining := tc.get(buffer); len(remaining) != 0 {
				t.Fatalf("remaining after segment drain = %d, want 0", len(remaining))
			}
		})
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
	if strings.Contains(plain, "Thinking") {
		t.Fatalf("render = %q, want no standalone Thinking label", plain)
	}
}

// delegationStates returns all delegations in the buffer, both singles and
// group entries, in forward order. Singles come from segmentDelegation;
// group entries come from segmentDelegationGroup in their group order.
func delegationStates(b *contentBuffer) []*delegationDisplayState {
	var result []*delegationDisplayState
	for _, seg := range b.segments {
		if seg.kind == segmentDelegation && seg.delegData != nil {
			result = append(result, seg.delegData)
		} else if seg.kind == segmentDelegationGroup && seg.delegGroupData != nil {
			result = append(result, seg.delegGroupData.entries...)
		}
	}
	return result
}

func TestConsecutiveSpecialistDelegateCallsMergeIntoGroup(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}

	// First specialist tool call
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "do stuff"}))
	// First delegation starts
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "do stuff"))
	// First delegation completes
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "child-1",
		Status:        "complete",
		TurnCount:     1,
		TokenCount:    100,
		ToolCallCount: 0,
		Output:        "done",
	}))

	// Second specialist tool call
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_2", map[string]any{"task": "do more stuff"}))
	// Second delegation starts
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "do more stuff"))

	// Should have exactly one segment of kind segmentDelegationGroup
	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	seg := buffer.segments[0]
	if seg.kind != segmentDelegationGroup {
		t.Fatalf("segment kind = %v, want segmentDelegationGroup", seg.kind)
	}
	if seg.delegGroupData == nil {
		t.Fatal("delegGroupData = nil")
	}
	if len(seg.delegGroupData.entries) != 2 {
		t.Fatalf("group entries = %d, want 2", len(seg.delegGroupData.entries))
	}
	if seg.delegGroupData.entries[0].agentID != "child-1" || seg.delegGroupData.entries[1].agentID != "child-2" {
		t.Errorf("entries agentIDs = %q %q, want child-1 child-2",
			seg.delegGroupData.entries[0].agentID, seg.delegGroupData.entries[1].agentID)
	}
}

func TestThreeConsecutiveDelegateCallsWithActiveMiddleMergeIntoGroup(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}

	// Three consecutive specialist delegations regardless of middle status
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "child-1",
		Status:        "complete",
		TurnCount:     1,
		TokenCount:    100,
		ToolCallCount: 0,
		Output:        "done",
	}))

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_2", map[string]any{"task": "second"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "second"))
	// Leave child-2 active (do not send Complete)

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_3", map[string]any{"task": "third"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-3", "third"))

	// All three should be in one group
	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	seg := buffer.segments[0]
	if seg.kind != segmentDelegationGroup {
		t.Fatalf("segment kind = %v, want segmentDelegationGroup", seg.kind)
	}
	if len(seg.delegGroupData.entries) != 3 {
		t.Fatalf("group entries = %d, want 3", len(seg.delegGroupData.entries))
	}
	// Middle one should still be active
	if seg.delegGroupData.entries[1].status != "active" {
		t.Fatalf("entry[1].status = %q, want active", seg.delegGroupData.entries[1].status)
	}
}

func TestSpecialistAdvisorSpecialistProducesThreeSeparateSegments(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}

	// First specialist
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))

	// Advisor
	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 1, "", nil))

	// Second specialist
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_2", map[string]any{"task": "second"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "second"))

	// Should have exactly 3 segments: specialist, advisor, specialist
	if len(buffer.segments) != 3 {
		t.Fatalf("segments count = %d, want 3", len(buffer.segments))
	}
	if buffer.segments[0].kind != segmentDelegation {
		t.Fatalf("segment[0].kind = %v, want segmentDelegation", buffer.segments[0].kind)
	}
	if buffer.segments[1].kind != segmentDelegation {
		t.Fatalf("segment[1].kind = %v, want segmentDelegation (advisor)", buffer.segments[1].kind)
	}
	if !buffer.segments[1].delegData.isAdvisor {
		t.Error("segment[1] should be advisor")
	}
	if buffer.segments[2].kind != segmentDelegation {
		t.Fatalf("segment[2].kind = %v, want segmentDelegation", buffer.segments[2].kind)
	}

	// activeAdvisorSegment should point to the correct segment (1-based: segment 2)
	if buffer.activeAdvisorSegment != 2 {
		t.Fatalf("activeAdvisorSegment = %d, want 2", buffer.activeAdvisorSegment)
	}
}

func TestTwoConsecutiveAdvisorCallsStayAsSeparateSegments(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:          make([]contentSegment, 0),
		collapseState:     make(map[int]bool),
		activeDelegations: make(map[string]delegationLocator),
		styles:            testStyles(theme.AccentAmber),
	}

	// First advisor
	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-1", 1, 1, "", nil))
	buffer.AppendEvent(output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{Model: "advisor-1", UseNumber: 1, MaxUses: 1}))

	// Second advisor
	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-2", 1, 1, "", nil))

	// Find delegation segments (ignore other types)
	var delegSegs []contentSegment
	for _, seg := range buffer.segments {
		if seg.kind == segmentDelegation && seg.delegData != nil && seg.delegData.isAdvisor {
			delegSegs = append(delegSegs, seg)
		}
	}

	// Should have exactly 2 separate advisor segments, never a group
	if len(delegSegs) != 2 {
		t.Fatalf("advisor delegation segments = %d, want 2", len(delegSegs))
	}
	if delegSegs[0].delegGroupData != nil {
		t.Error("segment[0] should not have delegGroupData")
	}
	if delegSegs[1].delegGroupData != nil {
		t.Error("segment[1] should not have delegGroupData")
	}
}

func TestSpecialistBashSpecialistProducesThreeSegments(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}

	// First specialist
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))

	// Regular bash tool call (not a delegate)
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "bash_1", map[string]any{"command": "echo hi"}))
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "bash", "bash_1", "hi\n", nil))

	// Second specialist
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_2", map[string]any{"task": "second"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "second"))

	// Should have 3 segments: delegation, tool call, delegation
	if len(buffer.segments) != 3 {
		t.Fatalf("segments count = %d, want 3", len(buffer.segments))
	}
	if buffer.segments[0].kind != segmentDelegation {
		t.Fatalf("segment[0].kind = %v, want segmentDelegation", buffer.segments[0].kind)
	}
	if buffer.segments[1].kind != segmentToolCall {
		t.Fatalf("segment[1].kind = %v, want segmentToolCall", buffer.segments[1].kind)
	}
	if buffer.segments[2].kind != segmentDelegation {
		t.Fatalf("segment[2].kind = %v, want segmentDelegation", buffer.segments[2].kind)
	}
}

func TestSameLabelGroupRendersLabelBorderColor(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}

	// Create a group with same toolLabel
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_1", map[string]any{"task": "explore"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "explore first"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_2", map[string]any{"task": "explore more"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "explore second"))

	if len(buffer.segments) != 1 || buffer.segments[0].kind != segmentDelegationGroup {
		t.Fatalf("expected one delegationGroup segment")
	}

	group := buffer.segments[0].delegGroupData
	label := delegationGroupBorderLabel(group)
	if label != "explore" {
		t.Fatalf("border label = %q, want explore", label)
	}

	// Test direct function
	_, style1 := buffer.delegationStyles("explore")
	_, style2 := buffer.delegationStyles("") // default
	if style1.GetForeground() == style2.GetForeground() {
		t.Error("same-label and default border colors should differ")
	}
}

func TestMixedLabelGroupRendersDefaultBorderColor(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}

	// Create a group with different toolLabels by creating entries directly
	dd1 := &delegationDisplayState{
		agentID:     "child-1",
		toolLabel:   "explore",
		taskPreview: "explore first",
		status:      "complete",
		collapsed:   true,
	}
	dd2 := &delegationDisplayState{
		agentID:     "child-2",
		toolLabel:   "implement",
		taskPreview: "implement second",
		status:      "complete",
		collapsed:   true,
	}

	group := &delegationGroupSegment{
		entries: []*delegationDisplayState{dd1, dd2},
	}

	label := delegationGroupBorderLabel(group)
	if label != "" {
		t.Fatalf("border label = %q, want empty string for mixed labels", label)
	}

	// Render and verify it uses the default border style
	buffer.segments = append(buffer.segments, contentSegment{
		kind:           segmentDelegationGroup,
		delegGroupData: group,
		renderDirty:    true,
	})

	rendered := buffer.renderDelegationGroupSegment(buffer.segments[0], 50)
	if rendered == "" {
		t.Fatalf("rendered output is empty")
	}
	// Just verify it renders without error and contains entries
	if !strings.Contains(rendered, "child-1") || !strings.Contains(rendered, "child-2") {
		t.Errorf("rendered output missing entries: %q", rendered)
	}
}

func TestTwoDelegationStartedEventsBeforeParentToolCallsBindCorrectly(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}

	// Start both tool calls before delegations
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "task1"}))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_2", map[string]any{"task": "task2"}))

	// Both delegations arrive
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "task1"))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "task2"))

	// Should have one group with 2 entries
	if len(buffer.segments) != 1 || buffer.segments[0].kind != segmentDelegationGroup {
		t.Fatalf("expected one delegationGroup")
	}

	group := buffer.segments[0].delegGroupData
	if len(group.entries) != 2 {
		t.Fatalf("group entries = %d, want 2", len(group.entries))
	}

	// Verify each entry got the correct parentCallID and promptText
	if group.entries[0].parentCallID != "call_1" {
		t.Errorf("entry[0].parentCallID = %q, want call_1", group.entries[0].parentCallID)
	}
	if group.entries[1].parentCallID != "call_2" {
		t.Errorf("entry[1].parentCallID = %q, want call_2", group.entries[1].parentCallID)
	}
	if group.entries[0].agentID != "child-1" {
		t.Errorf("entry[0].agentID = %q, want child-1", group.entries[0].agentID)
	}
	if group.entries[1].agentID != "child-2" {
		t.Errorf("entry[1].agentID = %q, want child-2", group.entries[1].agentID)
	}
}

func TestDelegationExtensionEventUpdatesCorrectEntryInGroup(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}

	// Create a group with 2 entries
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_2", map[string]any{"task": "second"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "second"))

	// Send DelegationExtensionEvent for entry 0
	buffer.AppendEvent(output.Event{
		Type: output.EventTypeDelegationExtension,
		Payload: output.DelegationExtensionEvent{
			AgentID:       "child-1",
			Extension:     3,
			MaxExtensions: 5,
		},
	})

	group := buffer.segments[0].delegGroupData
	// Entry 0 should be updated
	if group.entries[0].extCurrent != 3 || group.entries[0].extMax != 5 {
		t.Errorf("entry[0] ext = %d/%d, want 3/5", group.entries[0].extCurrent, group.entries[0].extMax)
	}
	// Entry 1 should be unchanged
	if group.entries[1].extCurrent != 0 || group.entries[1].extMax != 5 {
		t.Errorf("entry[1] ext = %d/%d, want 0/5", group.entries[1].extCurrent, group.entries[1].extMax)
	}
}

func TestToolCallFinishedWithErrorMarksonlyGroupEntryFailed(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}

	// Create a group: both start as active, first one gets bound to a tool
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))
	// Don't complete, let it stay active
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_2", map[string]any{"task": "second"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "second"))

	// Finish call_1 with error
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "code", "call_1", "", errors.New("error1")))

	group := buffer.segments[0].delegGroupData
	// Entry 0 should still be active (it was already started, so the error is just dropped)
	if group.entries[0].status != "active" {
		t.Errorf("entry[0].status = %q, want active", group.entries[0].status)
	}
	// Entry 1 should still be active
	if group.entries[1].status != "active" {
		t.Errorf("entry[1].status = %q, want active", group.entries[1].status)
	}
}

func TestDelegationGroupClickMathOnEntry1Header(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}

	// Create a two-entry group
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_2", map[string]any{"task": "second"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "second"))

	if len(buffer.segments) != 1 || buffer.segments[0].kind != segmentDelegationGroup {
		t.Fatalf("expected one delegationGroup")
	}

	group := buffer.segments[0].delegGroupData
	if len(group.entries) != 2 {
		t.Fatalf("expected 2 entries")
	}

	// Test delegationGroupEntryAtRow mapping:
	// The row numbering within the segment is:
	// Row 0 = box top border (returns -1, -1)
	// Row 1 = entry 0's first content row (header)
	// Rows 2+ = entry 0's remaining content rows
	// Row after entry 0 = divider (returns -1, -1)
	// Row after divider = entry 1's first content row (header)
	// Rows after that = entry 1's remaining content rows

	// Row 0 = box top border (returns -1, -1)
	entry, rowInEntry := buffer.delegationGroupEntryAtRow(group, 0, 76)
	if entry != -1 || rowInEntry != -1 {
		t.Fatalf("row 0 (top border) should return (-1, -1), got (%d, %d)", entry, rowInEntry)
	}

	// Row 1 = entry 0's first content row (header, returns 0, 0)
	entry, rowInEntry = buffer.delegationGroupEntryAtRow(group, 1, 76)
	if entry != 0 || rowInEntry != 0 {
		t.Fatalf("row 1 (entry 0 header) should return (0, 0), got (%d, %d)", entry, rowInEntry)
	}

	// Calculate row for entry 1's header
	entry0ContentRows := len(buffer.delegationContentRows(group.entries[0], 76))
	// After entry 0's content (entry0ContentRows rows starting at row 1) comes the divider
	// rowInSegment = 1 + entry0ContentRows (divider row)
	// rowInSegment = 1 + entry0ContentRows + 1 (entry 1's header)
	rowOfEntry1Header := 1 + entry0ContentRows + 1

	// Verify entry 1's header maps correctly to (1, 0)
	entry, rowInEntry = buffer.delegationGroupEntryAtRow(group, rowOfEntry1Header, 76)
	if entry != 1 || rowInEntry != 0 {
		t.Fatalf("row %d (entry 1 header) should return (1, 0), got (%d, %d)", rowOfEntry1Header, entry, rowInEntry)
	}

	// Test divider row (should return -1, -1)
	dividerRow := 1 + entry0ContentRows // row right before entry1Header
	entry, rowInEntry = buffer.delegationGroupEntryAtRow(group, dividerRow, 76)
	if entry != -1 || rowInEntry != -1 {
		t.Fatalf("divider row %d should return (-1, -1), got (%d, %d)", dividerRow, entry, rowInEntry)
	}
}

func TestCheckBufferDirtyWithActiveEntryInGroup(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}

	// Create a group with one active and one complete
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_2", map[string]any{"task": "second"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "second"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "child-2",
		Status:        "complete",
		TurnCount:     1,
		TokenCount:    100,
		ToolCallCount: 0,
		Output:        "done",
	}))

	group := buffer.segments[0].delegGroupData
	if group.entries[0].status != "active" {
		t.Fatalf("entry 0 should be active")
	}
	if group.entries[1].status != "complete" {
		t.Fatalf("entry 1 should be complete")
	}

	seg := &buffer.segments[0]
	// Check if segment needs rendering due to active entry
	isDirty := segmentHasActiveDelegation(seg)
	if !isDirty {
		t.Error("segment should be dirty/need render when it has an active delegation")
	}
}

func TestDelegationStartedBindsPendingBoxByCallID(t *testing.T) {
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "first"}))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_2", map[string]any{"task": "second"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "second preview", "call_2"))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first preview", "call_1"))

	if len(buffer.segments) != 1 || buffer.segments[0].delegGroupData == nil {
		t.Fatalf("segments = %#v, want one delegation group", buffer.segments)
	}
	entries := buffer.segments[0].delegGroupData.entries
	if len(entries) != 2 {
		t.Fatalf("delegation entries = %d, want 2", len(entries))
	}
	if entries[0].agentID != "child-1" || entries[0].taskPreview != "first preview" {
		t.Errorf("first entry = (%q, %q), want (child-1, first preview)", entries[0].agentID, entries[0].taskPreview)
	}
	if entries[1].agentID != "child-2" || entries[1].taskPreview != "second preview" {
		t.Errorf("second entry = (%q, %q), want (child-2, second preview)", entries[1].agentID, entries[1].taskPreview)
	}
}

func TestDelegationStartedEmptyCallIDUsesFIFO(t *testing.T) {
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call_1", map[string]any{"task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first preview"))

	if len(buffer.segments) != 1 || buffer.segments[0].delegData == nil {
		t.Fatalf("delegation segment missing")
	}
	if got := buffer.segments[0].delegData.agentID; got != "child-1" {
		t.Errorf("agentID = %q, want child-1", got)
	}
}

func TestActiveDelegateRowsUseTranscriptOrderAndLifecycleType(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.WithAgentScope(output.NewDelegationStartedEventWithType("child-2", "second task", "", "", "review"), "child-2"))
	buffer.AppendEvent(output.WithAgentScope(output.NewDelegationStartedEventWithType("child-1", "first task", "", "", "explore"), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewDelegationStartedEventWithType("child-3", "third task", "", "", "code"), "child-3"))

	rows := buffer.ActiveDelegateRows()
	if len(rows) != 3 {
		t.Fatalf("active rows = %d, want 3", len(rows))
	}
	want := []delegateActiveRow{
		{agentID: "child-2", agentType: "review", taskPreview: "second task"},
		{agentID: "child-1", agentType: "explore", taskPreview: "first task"},
		{agentID: "child-3", agentType: "code", taskPreview: "third task", isCode: true},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("active rows = %#v, want %#v", rows, want)
	}
}

func TestActiveDelegateRowsExcludeCompletedAndFailed(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewDelegationStartedEventWithType("complete", "done task", "", "", "code"))
	buffer.AppendEvent(output.NewDelegationStartedEventWithType("failed", "failed task", "", "", "review"))
	buffer.AppendEvent(output.NewDelegationStartedEventWithType("active", "live task", "", "", "explore"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{AgentID: "complete", Status: "complete"}))
	buffer.AppendEvent(output.NewDelegationFailedEvent("failed", "failed task", "error"))

	rows := buffer.ActiveDelegateRows()
	if len(rows) != 1 {
		t.Fatalf("active rows = %d, want 1", len(rows))
	}
	if rows[0].agentID != "active" || rows[0].agentType != "explore" {
		t.Fatalf("active row = %#v, want active explore row", rows[0])
	}
}

func TestActiveDelegateRowsIncludeCacheWaitingAndExcludeCancellation(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call-cache", map[string]any{"task": "cache task"}))
	buffer.AppendEvent(output.NewDelegationCacheWaitingEvent("cache-child", "call-cache", time.Now().Add(time.Second)))
	buffer.AppendEvent(output.NewDelegationStartedEventWithType("cache-child", "cache task", "call-cache", "", "code"))

	rows := buffer.ActiveDelegateRows()
	if len(rows) != 1 || rows[0].agentID != "cache-child" || rows[0].agentType != "code" || !rows[0].isCode {
		t.Fatalf("cache-waiting rows = %#v, want active code row", rows)
	}

	buffer.AppendEvent(output.WithAgentScope(output.NewStopReasonEvent(1, "cancelled", nil), "cache-child"))
	if rows := buffer.ActiveDelegateRows(); len(rows) != 0 {
		t.Fatalf("rows after cancellation = %#v, want none", rows)
	}
}

func TestActiveDelegateRowsLegacyTypeFallsBackToToolLabel(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "call-code", map[string]any{"task": "code task"}))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call-explore", map[string]any{"task": "explore task"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("code-child", "code task", "call-code"))
	buffer.AppendEvent(output.NewDelegationStartedEvent("explore-child", "explore task", "call-explore"))

	rows := buffer.ActiveDelegateRows()
	if len(rows) != 2 {
		t.Fatalf("legacy active rows = %d, want 2", len(rows))
	}
	if rows[0].agentType != "code" || !rows[0].isCode {
		t.Fatalf("legacy code row = %#v, want toolLabel code and isCode true", rows[0])
	}
	if rows[1].agentType != "explore" || rows[1].isCode {
		t.Fatalf("legacy explore row = %#v, want toolLabel explore and isCode false", rows[1])
	}
}

func TestActiveDelegateRowsExcludeAdvisorsAndEmptyIDs(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 2, "", nil))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "code", "pending-call", map[string]any{"task": "pending task"}))

	if rows := buffer.ActiveDelegateRows(); len(rows) != 0 {
		t.Fatalf("rows with advisor and pending delegate = %#v, want none", rows)
	}
}

func TestActiveDelegateRowsPreserveGroupEntryOrder(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewDelegationStartedEventWithType("child-1", "first task", "", "", "explore"))
	buffer.AppendEvent(output.NewDelegationStartedEventWithType("child-2", "second task", "", "", "code"))

	if len(buffer.segments) != 1 || buffer.segments[0].kind != segmentDelegationGroup {
		t.Fatalf("segments = %#v, want one delegation group", buffer.segments)
	}
	rows := buffer.ActiveDelegateRows()
	if len(rows) != 2 {
		t.Fatalf("group active rows = %d, want 2", len(rows))
	}
	if rows[0].agentID != "child-1" || rows[1].agentID != "child-2" {
		t.Fatalf("group rows = %#v, want child-1 then child-2", rows)
	}
}
