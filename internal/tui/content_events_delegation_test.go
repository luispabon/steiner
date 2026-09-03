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

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "inspect cache"}))
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call-production", map[string]any{"type": "code", "task": "wait for cache"}))
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "wait for cache"}))
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
	buffer.AppendEvent(output.NewDelegationFailedEvent(output.DelegationFailedParams{AgentID: "child-1", TaskPreview: "first task", Error: "late failure"}))
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
	buffer.AppendEvent(output.NewDelegationFailedEvent(output.DelegationFailedParams{AgentID: "unknown-failed", TaskPreview: "unknown task", Error: "error"}))

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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "do stuff"}))

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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "do stuff"}))
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

// TestDelegationStarted_ConcurrentFollowUps_BindByAgentIDNotFIFO guards
// against the bug in https://github.com/luispabon/steiner/issues/593: when
// two follow_up boxes are pending at once and a DelegationStartedEvent's
// CallID does not match either of them (e.g. a producer bug reusing a stale
// ParentCallID), the CallID-based bind must not fall back to blind FIFO
// ordering — that misroutes the event, and every scoped stream chunk that
// follows, into an unrelated agent's box.
func TestDelegationStarted_ConcurrentFollowUps_BindByAgentIDNotFIFO(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		styles:                 testStyles(theme.AccentAmber),
	}

	// Two follow_up calls are in flight together: child-3 first, then child-5.
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "follow_up", "call_fu_3", map[string]any{"agent_id": "child-3", "message": "continue child-3"}))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "follow_up", "call_fu_5", map[string]any{"agent_id": "child-5", "message": "continue child-5"}))
	if len(buffer.pendingDelegateParents) != 2 {
		t.Fatalf("pendingDelegateParents len = %d, want 2", len(buffer.pendingDelegateParents))
	}

	// child-5's DelegationStartedEvent arrives with a CallID that matches
	// neither pending entry's parentCallID (simulating a stale ParentCallID
	// carried over from the original delegate call). It must still bind to
	// the child-5 box, not the first (child-3) box in the queue.
	started := output.NewDelegationStartedEventWithType("child-5", "continue child-5", "stale-call-id", "", "")
	started = output.WithAgentScope(started, "child-5")
	buffer.AppendEvent(started)

	if len(buffer.pendingDelegateParents) != 1 {
		t.Fatalf("pendingDelegateParents len = %d, want 1 after bind", len(buffer.pendingDelegateParents))
	}
	if buffer.pendingDelegateParents[0].dd.followUpAgentID != "child-3" {
		t.Fatalf("remaining pending entry followUpAgentID = %q, want %q", buffer.pendingDelegateParents[0].dd.followUpAgentID, "child-3")
	}

	// Adjacent delegation boxes merge into a single delegationGroup segment.
	if len(buffer.segments) != 1 || buffer.segments[0].delegGroupData == nil {
		t.Fatalf("segments = %#v, want 1 segmentDelegationGroup", buffer.segments)
	}
	entries := buffer.segments[0].delegGroupData.entries
	if len(entries) != 2 {
		t.Fatalf("delegationGroup entries = %d, want 2", len(entries))
	}
	child3Box, child5Box := entries[0], entries[1]
	if child3Box.agentID != "" {
		t.Fatalf("child-3 box agentID = %q, want empty (must remain unbound)", child3Box.agentID)
	}
	if child5Box.agentID != "child-5" {
		t.Fatalf("child-5 box agentID = %q, want %q", child5Box.agentID, "child-5")
	}

	// A scoped assistant chunk for child-5 must land in child-5's box, not
	// child-3's — this is the observable symptom from the issue.
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "child-5 output", output.ChunkSourceAssistant), "child-5"))
	if child5Box.lastEntry() == nil || !strings.Contains(child5Box.lastEntry().body, "child-5 output") {
		t.Fatalf("child-5 box did not receive its scoped chunk: %#v", child5Box.entries)
	}
	if child3Box.lastEntry() != nil {
		t.Fatalf("child-3 box unexpectedly received a chunk meant for child-5: %#v", child3Box.entries)
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "do stuff"}))
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_2", map[string]any{"type": "code", "task": "do more stuff"}))
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "child-1",
		Status:        "complete",
		TurnCount:     1,
		TokenCount:    100,
		ToolCallCount: 0,
		Output:        "done",
	}))

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_2", map[string]any{"type": "code", "task": "second"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "second"))
	// Leave child-2 active (do not send Complete)

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_3", map[string]any{"type": "code", "task": "third"}))
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))

	// Advisor
	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 1, "", nil))

	// Second specialist
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_2", map[string]any{"type": "code", "task": "second"}))
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))

	// Regular bash tool call (not a delegate)
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "bash_1", map[string]any{"command": "echo hi"}))
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "bash", "bash_1", "hi\n", nil))

	// Second specialist
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_2", map[string]any{"type": "code", "task": "second"}))
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "explore", "task": "explore"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "explore first"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_2", map[string]any{"type": "explore", "task": "explore more"}))
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "task1"}))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_2", map[string]any{"type": "code", "task": "task2"}))

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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_2", map[string]any{"type": "code", "task": "second"}))
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))
	// Don't complete, let it stay active
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_2", map[string]any{"type": "code", "task": "second"}))
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_2", map[string]any{"type": "code", "task": "second"}))
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "first"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_2", map[string]any{"type": "code", "task": "second"}))
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

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "first"}))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_2", map[string]any{"type": "code", "task": "second"}))
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code", "task": "first"}))
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
	buffer.AppendEvent(output.NewDelegationFailedEvent(output.DelegationFailedParams{AgentID: "failed", TaskPreview: "failed task", Error: "error"}))

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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call-cache", map[string]any{"type": "code", "task": "cache task"}))
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call-code", map[string]any{"type": "code", "task": "code task"}))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call-explore", map[string]any{"type": "explore", "task": "explore task"}))
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
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "pending-call", map[string]any{"type": "code", "task": "pending task"}))

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

func TestAdvisorEventRoutingToActiveChild(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewDelegationStartedEventWithType("child-id", "test task", "", "", "explore"))
	initialSegments := len(buffer.segments)

	event := output.NewAdvisorStartedEvent("advisor-model", 1, 1, "question", nil)
	event = output.WithAgentScope(event, "child-id")
	buffer.AppendEvent(event)

	if len(buffer.segments) != initialSegments {
		t.Errorf("scoped advisor event appended new segment: before %d, after %d", initialSegments, len(buffer.segments))
	}

	loc, ok := buffer.activeDelegations["child-id"]
	if !ok {
		t.Fatal("child delegation not in active delegations")
	}
	dd := loc.dd
	if dd.advisorBudget != 1 {
		t.Errorf("child advisorBudget = %d, want 1", dd.advisorBudget)
	}
	if dd.advisorQuestion != "question" {
		t.Errorf("child advisorQuestion = %q, want 'question'", dd.advisorQuestion)
	}
}

func TestAdvisorEventRoutingParentUnscoped(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewDelegationStartedEventWithType("child-id", "test task", "", "", "explore"))
	initialSegments := len(buffer.segments)

	event := output.NewAdvisorStartedEvent("advisor-model", 1, 1, "question", nil)
	buffer.AppendEvent(event)

	if len(buffer.segments) != initialSegments+1 {
		t.Errorf("unscoped advisor event did not append new segment: before %d, after %d", initialSegments, len(buffer.segments))
	}

	if buffer.segments[initialSegments].delegData == nil || !buffer.segments[initialSegments].delegData.isAdvisor {
		t.Fatal("new segment is not an advisor delegation")
	}
}

func TestAdvisorBudgetExhaustedRoutingToActiveChild(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewDelegationStartedEventWithType("child-id", "test task", "", "", "explore"))
	initialSegments := len(buffer.segments)

	event := output.NewAdvisorBudgetExhaustedEvent("advisor-model", 1, 1, "budget full", "question", nil)
	event = output.WithAgentScope(event, "child-id")
	buffer.AppendEvent(event)

	if len(buffer.segments) != initialSegments {
		t.Errorf("scoped budget exhausted event appended new segment: before %d, after %d", initialSegments, len(buffer.segments))
	}

	loc, ok := buffer.activeDelegations["child-id"]
	if !ok {
		t.Fatal("child delegation not in active delegations")
	}
	dd := loc.dd
	if dd.advisorDenied != 1 {
		t.Errorf("child advisorDenied = %d, want 1", dd.advisorDenied)
	}
}

func TestAdvisorBudgetExhaustedParentUnscoped(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewDelegationStartedEventWithType("child-id", "test task", "", "", "explore"))
	initialSegments := len(buffer.segments)

	event := output.NewAdvisorBudgetExhaustedEvent("advisor-model", 1, 1, "budget full", "question", nil)
	buffer.AppendEvent(event)

	if len(buffer.segments) != initialSegments+1 {
		t.Errorf("unscoped budget exhausted event did not append new segment: before %d, after %d", initialSegments, len(buffer.segments))
	}

	if buffer.segments[initialSegments].delegData == nil || !buffer.segments[initialSegments].delegData.isAdvisor {
		t.Fatal("new segment is not an advisor delegation")
	}
}

// TestAdvisorThinkingChunkScopedToActiveChildMutatesChildState pins I1: an
// advisor thinking chunk scoped to an active child agent ID is already
// routed by the generic scoped-delegation event path (AppendEvent line 397
// -> appendScopedDelegationEvent -> applyDelegationThinkingChunk) added in
// "Step 7: Complete nested advisor attribution with scoped budget tracking".
// It mutates that child's delegationDisplayState and appends no new
// top-level segment; handleAdvisorThinkingChunk's activeAdvisorSegment
// fallback is unreachable for this case. No production code change is
// needed for I1 — this test is a regression pin.
func TestAdvisorThinkingChunkScopedToActiveChildMutatesChildState(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.showThinking = true
	buffer.AppendEvent(output.NewDelegationStartedEventWithType("child-id", "test task", "", "", "review"))
	initialSegments := len(buffer.segments)

	event := output.NewThinkingChunkEventWithSource(0, "advisor reasoning text", output.ChunkSourceAdvisor)
	event = output.WithAgentScope(event, "child-id")
	buffer.AppendEvent(event)

	if len(buffer.segments) != initialSegments {
		t.Errorf("scoped advisor thinking chunk appended new segment: before %d, after %d", initialSegments, len(buffer.segments))
	}

	loc, ok := buffer.activeDelegations["child-id"]
	if !ok {
		t.Fatal("child delegation not in active delegations")
	}
	dd := loc.dd
	if len(dd.entries) != 1 {
		t.Fatalf("child entries = %d, want 1", len(dd.entries))
	}
	entry := dd.entries[0]
	if entry.source != output.ChunkSourceAdvisor {
		t.Errorf("entry source = %q, want %q", entry.source, output.ChunkSourceAdvisor)
	}
	if entry.body != "advisor reasoning text" {
		t.Errorf("entry body = %q, want %q", entry.body, "advisor reasoning text")
	}
	if buffer.activeAdvisorSegment != 0 {
		t.Errorf("activeAdvisorSegment = %d, want 0 (unscoped fallback not used)", buffer.activeAdvisorSegment)
	}
}

// TestAdvisorThinkingChunkUnscopedUsesActiveAdvisorSegment is the mirror of
// the above: an unscoped advisor thinking chunk keeps today's behaviour,
// routing through the package-global activeAdvisorSegment fallback.
func TestAdvisorThinkingChunkUnscopedUsesActiveAdvisorSegment(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.showThinking = true
	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 1, "question", nil))
	if buffer.activeAdvisorSegment == 0 {
		t.Fatal("activeAdvisorSegment not set after unscoped advisor started")
	}
	initialSegments := len(buffer.segments)

	event := output.NewThinkingChunkEventWithSource(0, "unscoped advisor reasoning", output.ChunkSourceAdvisor)
	buffer.AppendEvent(event)

	if len(buffer.segments) != initialSegments {
		t.Errorf("unscoped advisor thinking chunk appended new segment: before %d, after %d", initialSegments, len(buffer.segments))
	}

	idx := buffer.activeAdvisorSegment - 1
	dd := buffer.segments[idx].delegData
	if dd == nil || !dd.isAdvisor {
		t.Fatal("active advisor segment missing or not an advisor delegation")
	}
	if len(dd.entries) != 1 || dd.entries[0].body != "unscoped advisor reasoning" {
		t.Fatalf("advisor segment entries = %#v, want one entry with the chunk body", dd.entries)
	}
}

// TestHandleDelegationFailedCarriesAdvisorCountersForActiveChild is the B2
// TUI-side test: a DelegationFailedEvent for an active child with non-zero
// advisor counters updates the child's delegationDisplayState so the
// rendered failed meta line surfaces "advisor n/m".
func TestHandleDelegationFailedCarriesAdvisorCountersForActiveChild(t *testing.T) {
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewDelegationStartedEventWithType("child-id", "test task", "", "", "review"))

	event := output.NewDelegationFailedEvent(output.DelegationFailedParams{
		AgentID:       "child-id",
		TaskPreview:   "test task",
		Error:         "boom",
		AdvisorBudget: 3,
		AdvisorUses:   2,
		AdvisorDenied: 1,
	})
	event = output.WithAgentScope(event, "child-id")
	buffer.AppendEvent(event)

	loc, ok := buffer.findDelegation("child-id")
	if !ok {
		t.Fatal("failed child delegation not found")
	}
	dd := loc.dd
	if dd.status != "failed" {
		t.Errorf("status = %q, want failed", dd.status)
	}
	if dd.advisorBudget != 3 || dd.advisorUses != 2 || dd.advisorDenied != 1 {
		t.Errorf("advisor counters = %d/%d/%d, want 3/2/1", dd.advisorBudget, dd.advisorUses, dd.advisorDenied)
	}
}

// TestSubAgentWithTypeArgumentRendersByType verifies that sub_agent calls
// with a type argument extract and use that type as the toolLabel,
// resulting in correct per-type text labels and colors.
func TestSubAgentWithTypeArgumentRendersByType(t *testing.T) {
	t.Parallel()
	types := []string{"explore", "research", "code", "evaluate", "sanity_check", "review", "vision"}
	for _, agentType := range types {
		t.Run(agentType, func(t *testing.T) {
			buffer := newTestBuffer(t)
			buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1",
				map[string]any{"type": agentType, "task": "test task"}))
			buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "preview"))

			loc, found := buffer.activeDelegations["child-1"]
			if !found || loc.dd == nil {
				t.Fatal("delegation not found")
			}
			dd := loc.dd
			if dd.toolLabel != agentType {
				t.Errorf("toolLabel = %q, want %q", dd.toolLabel, agentType)
			}
		})
	}
}

// TestSubAgentTypesConsecutiveGroupsByType verifies that two consecutive
// sub_agent calls with the same type group under that type's border label.
func TestSubAgentTypesConsecutiveGroupsByType(t *testing.T) {
	t.Parallel()
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1",
		map[string]any{"type": "code", "task": "task 1"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_2",
		map[string]any{"type": "code", "task": "task 2"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "second"))

	if len(buffer.segments) != 1 || buffer.segments[0].kind != segmentDelegationGroup {
		t.Fatalf("expected one delegationGroup segment")
	}

	group := buffer.segments[0].delegGroupData
	label := delegationGroupBorderLabel(group)
	if label != "code" {
		t.Errorf("border label = %q, want code", label)
	}
}

// TestSubAgentTypesMixedGroupsWithDefaultBorder verifies that two consecutive
// sub_agent calls with different types group with a default (empty label) border.
func TestSubAgentTypesMixedGroupsWithDefaultBorder(t *testing.T) {
	t.Parallel()
	buffer := newTestBuffer(t)
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1",
		map[string]any{"type": "explore", "task": "task 1"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "first"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_2",
		map[string]any{"type": "code", "task": "task 2"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "second"))

	if len(buffer.segments) != 1 || buffer.segments[0].kind != segmentDelegationGroup {
		t.Fatalf("expected one delegationGroup segment")
	}

	group := buffer.segments[0].delegGroupData
	label := delegationGroupBorderLabel(group)
	if label != "" {
		t.Errorf("border label = %q, want empty string for mixed types", label)
	}
}

// TestFollowUpToUnfindableDelegationFallsBackToAgentType covers issue #656:
// a follow_up call targeting an agent whose original spawn isn't in this
// transcript (e.g. resumed from a session recorded before the specialized
// delegate tools were consolidated into sub_agent, so the spawn call used a
// bare tool name like "review" that isn't recognized as a delegate call).
// findChildDelegationInfo can't recover a toolLabel from a record that was
// never created, but DelegationStartedEvent's AgentType is authoritative
// (sourced from the persisted child session, not the transcript), so the
// header must render using it instead of a blank label.
func TestFollowUpToUnfindableDelegationFallsBackToAgentType(t *testing.T) {
	t.Parallel()
	buffer := newTestBuffer(t)

	// The original spawn is NOT replayed as a specialized delegate tool call
	// (simulating a pre-consolidation session), so it renders as a plain
	// tool call and never becomes a findable delegation record.
	buffer.AppendEvent(output.NewToolCallStartedEvent(0, "review", "call_1",
		map[string]any{"task": "review the diff"}))

	buffer.AppendEvent(output.NewToolCallStartedEvent(0, "follow_up", "call_2",
		map[string]any{"agent_id": "child-1", "message": "check again"}))
	buffer.AppendEvent(output.NewDelegationStartedEventWithType("child-1", "preview", "call_2", "", "review"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID: "child-1", Status: "complete",
	}))

	loc, found := buffer.findDelegation("child-1")
	if !found || loc.dd == nil {
		t.Fatal("delegation not found")
	}
	if got := loc.dd.effectiveTypeLabel(); got != "review" {
		t.Errorf("effectiveTypeLabel() = %q, want %q (toolLabel=%q agentType=%q)",
			got, "review", loc.dd.toolLabel, loc.dd.agentType)
	}
	_, borderStyle := buffer.delegationStyles(loc.dd.effectiveTypeLabel())
	if borderStyle.GetForeground() == buffer.styles.DelegateBorderDefault.GetForeground() {
		t.Error("border style fell back to default; want the review-specific border")
	}
}
