package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestBuildDeliveredRows(t *testing.T) {
	t.Parallel()
	grp := func(_ output.DeliveredSubAgent) deliveredLookup {
		return deliveredLookup{group: "final-review", groupSize: 3}
	}
	failed := func(_ output.DeliveredSubAgent) deliveredLookup {
		return deliveredLookup{reason: "make check: 2 failing packages\nmore detail"}
	}
	item := func(id, typ, status string, ms int64) output.DeliveredSubAgent {
		return output.DeliveredSubAgent{AgentID: id, AgentType: typ, Status: status, DurationMs: ms}
	}
	tests := []struct {
		name       string
		items      []output.DeliveredSubAgent
		lookup     func(output.DeliveredSubAgent) deliveredLookup
		wantTag    string
		wantHeader string
		wantMember []string // substrings expected in member i
	}{
		{"single", []output.DeliveredSubAgent{item("child-6", "explore", "complete", 218000)}, nil,
			"sub-agent finished", "", []string{"explore child-6", "✓ complete", "3m38s"}},
		{"several ungrouped", []output.DeliveredSubAgent{item("c1", "code", "complete", 38000), item("c2", "code", "complete", 5000)}, nil,
			"sub-agents finished", "2 of 2", []string{"code c1", "38s"}},
		{"grouped", []output.DeliveredSubAgent{item("c4", "review", "complete", 1000), item("c5", "review", "complete", 2000)}, grp,
			"sub-agents finished", "group final-review (2 of 3)", []string{"review c4"}},
		{"failed with reason", []output.DeliveredSubAgent{item("c4", "sanity_check", "failed", 252000)}, failed,
			"sub-agent finished", "", []string{"✗ failed", "4m12s", "make check: 2 failing packages"}},
		{"lost", []output.DeliveredSubAgent{item("c9", "explore", "lost", 0)}, nil,
			"sub-agent finished", "", []string{"? lost (session restarted)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rows := buildDeliveredRows(tt.items, tt.lookup)
			if rows.tag != tt.wantTag || rows.header != tt.wantHeader {
				t.Fatalf("tag/header = %q/%q, want %q/%q", rows.tag, rows.header, tt.wantTag, tt.wantHeader)
			}
			if len(rows.members) != len(tt.items) {
				t.Fatalf("members = %d, want %d", len(rows.members), len(tt.items))
			}
			m := rows.members[0]
			got := strings.Join([]string{m.label, m.outcome, m.duration, m.reason}, " | ")
			for _, want := range tt.wantMember {
				if !strings.Contains(got, want) {
					t.Errorf("member %q missing %q", got, want)
				}
			}
			if strings.Contains(m.reason, "more detail") {
				t.Errorf("reason not clipped to first line: %q", m.reason)
			}
		})
	}
}

func newDeliveryBuffer() *contentBuffer {
	return &contentBuffer{
		collapseState:     make(map[int]bool),
		activeDelegations: make(map[string]delegationLocator),
		styles:            testStyles(theme.AccentAmber),
	}
}

func TestDeliveredEventRendersRowWithGroupAndReason(t *testing.T) {
	t.Parallel()
	b := newDeliveryBuffer()
	b.segments = append(b.segments,
		contentSegment{kind: segmentDelegation, delegData: &delegationDisplayState{agentID: "c4", parentCallID: "call-4", group: "g", status: "failed", failureReason: "boom: exploded\nstack", batch: 0}},
		contentSegment{kind: segmentDelegation, delegData: &delegationDisplayState{agentID: "c5", parentCallID: "call-5", group: "g", status: "complete", batch: 0}},
		contentSegment{kind: segmentDelegation, delegData: &delegationDisplayState{agentID: "c6", parentCallID: "call-6", group: "g", status: "active"}},
	)
	b.AppendEvent(output.Event{Type: output.EventTypeSubAgentsDelivered, Payload: output.SubAgentsDeliveredEvent{Items: []output.DeliveredSubAgent{
		{AgentID: "c4", AgentType: "review", Status: "failed", ParentCallID: "call-4", DurationMs: 4000},
		{AgentID: "c5", AgentType: "review", Status: "complete", ParentCallID: "call-5", DurationMs: 2000},
	}}})
	last := b.segments[len(b.segments)-1]
	if last.kind != segmentSubAgentsFinished {
		t.Fatalf("last segment kind = %v", last.kind)
	}
	out := stripANSI(b.renderSubAgentsFinishedSegment(last, 100))
	for _, want := range []string{"sub-agents finished", "group g (2 of 3)", "review c4", "✗ failed", "boom: exploded", "review c5", "✓ complete"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "stack") {
		t.Errorf("reason should be first line only:\n%s", out)
	}
}

func TestDeliveredLookupGroupSizeIsScopedToBatch(t *testing.T) {
	t.Parallel()
	b := newDeliveryBuffer()
	for i, callID := range []string{"old-1", "old-2", "new-1", "new-2"} {
		if i == 2 {
			b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "next batch"))
		}
		id := "c" + string(rune('1'+i))
		b.segments = append(b.segments, contentSegment{kind: segmentDelegation, delegData: &delegationDisplayState{
			agentID: id, parentCallID: callID, group: "reused", status: "complete", batch: b.delegationBatch,
		}})
	}
	items := []output.DeliveredSubAgent{
		{AgentID: "c3", AgentType: "review", Status: "complete", ParentCallID: "new-1"},
		{AgentID: "c4", AgentType: "review", Status: "complete", ParentCallID: "new-2"},
	}
	rows := buildDeliveredRows(items, b.deliveredLookup)
	if len(rows.members) != 2 || rows.header != "group reused (2 of 2)" {
		t.Fatalf("delivered rows = %+v, want group reused (2 of 2)", rows)
	}
	for _, item := range items {
		if got := b.deliveredLookup(item).groupSize; got != 2 {
			t.Errorf("groupSize for %s = %d, want 2", item.AgentID, got)
		}
	}
}

func TestTerminalEventFillsTypeAndDuration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		event    output.Event
		wantType string
		wantEl   string
	}{
		{"complete replay", output.NewDelegationCompleteEvent(output.DelegationCompleteParams{AgentID: "a", Status: "complete", AgentType: "explore", DurationMs: 65000}), "explore", "1m5s"},
		{"failed replay", output.NewDelegationFailedEvent(output.DelegationFailedParams{AgentID: "a", Error: "x", AgentType: "code", DurationMs: 2500}), "code", "2s"},
		{"live no duration", output.NewDelegationCompleteEvent(output.DelegationCompleteParams{AgentID: "a", Status: "complete"}), "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := newDeliveryBuffer()
			b.AppendEvent(tt.event)
			loc, ok := b.findDelegation("a")
			if !ok {
				t.Fatal("delegation not found")
			}
			if loc.dd.effectiveTypeLabel() != tt.wantType || loc.dd.elapsed != tt.wantEl {
				t.Fatalf("type/elapsed = %q/%q, want %q/%q", loc.dd.effectiveTypeLabel(), loc.dd.elapsed, tt.wantType, tt.wantEl)
			}
		})
	}
}

func TestSessionLoadedClearsStaleActivity(t *testing.T) {
	t.Parallel()
	m := newActivityTestModel(t)
	m.activity = m.activity.waiting("running tool", "read")
	m.applyEvent(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{Kind: "session_loaded", ContextWindow: 4096}))
	if m.activity.label != "" || m.activity.busy() {
		t.Fatalf("activity = %+v, want cleared", m.activity)
	}
}

func TestStrandedBody(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		count    int
		reason   string
		wantHead []string
		wantDet  []string
	}{
		{"reason singular", 1, "provider usage limit reached", []string{"not acted on", "turn failed: provider usage limit reached"}, []string{"1 sub-agent result is waiting"}},
		{"reason plural", 3, "boom", []string{"boom"}, []string{"3 sub-agent results are waiting"}},
		{"no reason", 2, "", []string{"not acted on", "2 sub-agent results waiting", "send a message"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			head, det := strandedBody(tt.count, tt.reason)
			for _, w := range tt.wantHead {
				if !strings.Contains(head, w) {
					t.Errorf("headline %q missing %q", head, w)
				}
			}
			if tt.wantDet == nil && det != "" {
				t.Errorf("detail = %q, want empty", det)
			}
			for _, w := range tt.wantDet {
				if !strings.Contains(det, w) {
					t.Errorf("detail %q missing %q", det, w)
				}
			}
		})
	}
}

func TestStrandedRowRenders(t *testing.T) {
	t.Parallel()
	b := newDeliveryBuffer()
	b.AppendEvent(output.Event{Type: output.EventTypeSubAgentResultsUnanswered, Payload: output.SubAgentResultsUnansweredEvent{AgentIDs: []string{"a"}, Reason: "limit"}})
	out := stripANSI(b.renderStrandedResultsSegment(b.segments[len(b.segments)-1], 100))
	for _, w := range []string{"⚠", "not acted on", "limit", "1 sub-agent result is waiting"} {
		if !strings.Contains(out, w) {
			t.Errorf("render missing %q:\n%s", w, out)
		}
	}
}

func TestStrandedActivityWarningSetsAndClears(t *testing.T) {
	t.Parallel()
	unanswered := output.Event{Type: output.EventTypeSubAgentResultsUnanswered, Payload: output.SubAgentResultsUnansweredEvent{AgentIDs: []string{"a", "b"}}}
	activityText := func(m *Model) string { return stripANSI(m.renderActivityRow(m.contentWidth())) }

	t.Run("cleared by generating", func(t *testing.T) {
		t.Parallel()
		m := newActivityTestModel(t)
		m.applyEvent(unanswered)
		if got := activityText(m); !strings.Contains(got, "2 sub-agent results not acted on") {
			t.Fatalf("activity row = %q, want stranded warning", got)
		}
		m.applyConversationState(output.ConversationStateEvent{State: conversationStateGenerating})
		if m.strandedResults != 0 {
			t.Fatalf("strandedResults = %d, want 0", m.strandedResults)
		}
		if got := activityText(m); strings.Contains(got, "not acted on") {
			t.Fatalf("activity row = %q, warning should be gone", got)
		}
	})
	t.Run("cleared by prompt submit", func(t *testing.T) {
		t.Parallel()
		m := newActivityTestModel(t)
		m.applyEvent(unanswered)
		m.executeSubmitAction("continue", "continue")
		if m.strandedResults != 0 {
			t.Fatalf("strandedResults = %d, want 0 after submit", m.strandedResults)
		}
	})
}
