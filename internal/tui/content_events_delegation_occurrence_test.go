package tui

import (
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestDelegationOccurrenceAdmissionFinishKeepsCardOwnership(t *testing.T) {
	for _, order := range []struct {
		name   string
		first  string
		second string
	}{
		{name: "accepted then rejected", first: "accepted", second: "rejected"},
		{name: "rejected then accepted", first: "rejected", second: "accepted"},
		{name: "unknown then accepted", first: "unknown", second: "accepted"},
		{name: "accepted then unknown", first: "accepted", second: "unknown"},
	} {
		t.Run(order.name, func(t *testing.T) {
			b := &contentBuffer{collapseState: make(map[int]bool)}
			b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "duplicate", map[string]any{"task": "first"}))
			b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "duplicate", map[string]any{"task": "second"}))
			first, second := b.segments[0].delegData, b.segments[1].delegData
			if first == nil || second == nil || first == second {
				t.Fatalf("duplicate cards = %p, %p", first, second)
			}

			for _, status := range []string{order.first, order.second} {
				var err error
				if status != "accepted" {
					err = errors.New("exact admission error")
				}
				b.AppendEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "duplicate", "", err, output.ToolPreview{}, &output.DelegationAdmission{Status: status}))
			}

			accepted := first
			if order.first != "accepted" {
				accepted = second
			}
			if findDelegationSegment(b.segments, accepted) < 0 {
				t.Fatalf("accepted pointer was not retained: first=%#v second=%#v", first, second)
			}
			if order.first == "unknown" || order.second == "unknown" {
				if first.status != "failed" && second.status != "failed" {
					t.Fatal("unknown finish did not target a card")
				}
			} else {
				rejected := second
				if accepted == second {
					rejected = first
				}
				if findDelegationSegment(b.segments, rejected) >= 0 {
					t.Fatalf("rejected pointer was retained: %p", rejected)
				}
			}
			if got := countToolText(b.segments, "exact admission error"); got != 1 {
				t.Fatalf("exact admission error count = %d, want 1", got)
			}
		})
	}
}

func TestDelegationOccurrenceReusedCallIDStartsNewLifecycle(t *testing.T) {
	b := &contentBuffer{collapseState: make(map[int]bool)}
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "reused", map[string]any{"task": "old"}))
	old := b.segments[0].delegData
	b.AppendEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "reused", "", errors.New("old rejection"), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "reused", map[string]any{"task": "new"}))
	fresh := b.segments[len(b.segments)-1].delegData
	if old == fresh || findDelegationSegment(b.segments, old) >= 0 {
		t.Fatalf("old occurrence survived reuse: old=%p fresh=%p", old, fresh)
	}
	b.AppendEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "reused", "", nil, output.ToolPreview{}, &output.DelegationAdmission{Status: "accepted", BatchID: "new-batch", Group: "new-group"}))
	if findDelegationSegment(b.segments, fresh) < 0 || fresh.group != "new-group" || b.segments[len(b.segments)-1].kind != segmentDelegationGroup {
		t.Fatalf("fresh occurrence was not accepted: %#v", fresh)
	}
}

func TestReplayDuplicateCallIDRejectedThenAcceptedTargetsSecondCard(t *testing.T) {
	t.Parallel()
	b := newGroupTestBuffer()
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "same", map[string]any{"task": "first"}))
	first := b.segments[0].delegData
	b.AppendEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "same", `{"ok":false,"error":{"message":"denied"}}`, errors.New("denied"), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))

	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "same", map[string]any{"task": "second"}))
	second := b.segments[len(b.segments)-1].delegData
	occ := output.DelegationOccurrence{CallID: "same", BatchID: "batch", AgentID: "agent-orphan"}
	b.AppendEvent(output.NewDelegationAcceptedEvent(occ, "group"))
	b.AppendEvent(output.NewDelegationStartedEvent(occ, "second", "", "explore"))

	if first != nil && findDelegationSegment(b.segments, first) >= 0 {
		t.Fatal("rejected first card remains")
	}
	if second == nil || second.agentID != "agent-orphan" || findDelegationSegment(b.segments, second) < 0 || second.status != "active" {
		t.Fatalf("accepted second card was changed or removed: %#v", second)
	}
	if got := b.activeDelegations["agent-orphan"].dd; got != second {
		t.Fatalf("active child card = %p, want second card %p", got, second)
	}
	if got := countDelegationCards(b.segments); got != 1 {
		t.Fatalf("delegation cards = %d, want 1", got)
	}
}

func TestDuplicateCallIDsAcrossBatchesSettleOwnCards(t *testing.T) {
	t.Parallel()
	b := newGroupTestBuffer()
	var cards [2]*delegationDisplayState
	for i, batch := range []string{"batch-1", "batch-2"} {
		agent := "agent-" + batch
		occ := output.DelegationOccurrence{CallID: "same", BatchID: batch, AgentID: agent}
		b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "same", subAgentArgs("review")))
		cards[i] = lastDelegationCard(b)
		b.AppendEvent(output.NewDelegationAcceptedEvent(occ, "review"))
		b.AppendEvent(output.NewDelegationQueuedEvent(occ, "explore", "task "+batch))
		b.AppendEvent(output.NewDelegationStartedEvent(occ, "task "+batch, "", "explore"))
	}
	if cards[0] == cards[1] || b.activeDelegations["agent-batch-1"].dd != cards[0] || b.activeDelegations["agent-batch-2"].dd != cards[1] {
		t.Fatalf("occurrences share a card: %p %p active=%#v", cards[0], cards[1], b.activeDelegations)
	}
	if len(b.segments) != 2 {
		t.Fatalf("segments = %v, want one frame per batch", segmentKinds(b.segments))
	}
	b.AppendEvent(output.NewDelegationFailedEvent(output.DelegationFailedParams{DelegationOccurrence: output.DelegationOccurrence{CallID: "same", BatchID: "batch-2", AgentID: "agent-batch-2"}, Error: "boom"}))
	b.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: output.DelegationOccurrence{CallID: "same", BatchID: "batch-1", AgentID: "agent-batch-1"}, Status: "completed"}))
	if cards[0].status != "complete" || cards[1].status != "failed" {
		t.Fatalf("statuses = %q/%q, want complete/failed", cards[0].status, cards[1].status)
	}
}

func lastDelegationCard(b *contentBuffer) *delegationDisplayState {
	var card *delegationDisplayState
	for _, tok := range flattenDelegationSegments(b.segments) {
		card = tok.dd
	}
	return card
}

func TestLostOccurrenceDoesNotConsumeFreshRejectedReuse(t *testing.T) {
	t.Parallel()
	b := newGroupTestBuffer()
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "same", map[string]any{"task": "old"}))
	b.AppendEvent(output.NewDelegationAcceptedEvent(output.DelegationOccurrence{CallID: "same", BatchID: "old-batch", AgentID: "child"}, "old-group"))
	b.AppendEvent(output.NewDelegationStartedEvent(output.DelegationOccurrence{CallID: "same", AgentID: "child"}, "old", "", "explore"))
	old := b.activeDelegations["child"].dd
	b.AppendEvent(output.Event{Type: output.EventTypeSubAgentsDelivered, Payload: output.SubAgentsDeliveredEvent{Items: []output.DeliveredSubAgent{{AgentID: "child", AgentType: "explore", Status: "lost", ParentCallID: "same"}}}})
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "same", map[string]any{"task": "fresh"}))
	var fresh *delegationDisplayState
	for _, token := range flattenDelegationSegments(b.segments) {
		if token.dd != nil && token.dd.taskPreview == "fresh" {
			fresh = token.dd
		}
	}
	if fresh == nil {
		t.Fatal("fresh delegation card missing")
	}
	b.AppendEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "same", `{"ok":false,"error":{"message":"fresh denied"}}`, errors.New("fresh denied"), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))

	if old.status != "failed" || old.resultStatus != "lost" {
		t.Fatalf("lost original = %#v", old)
	}
	if findDelegationSegment(b.segments, old) < 0 {
		t.Fatalf("lost original card disappeared: %#v", b.segments)
	}
	if findDelegationSegment(b.segments, fresh) >= 0 {
		t.Fatalf("fresh rejected card remains after reuse: %#v", b.segments)
	}
}

func countToolText(segments []contentSegment, text string) int {
	count := 0
	for _, segment := range segments {
		if segment.kind == segmentTool && segment.text == text {
			count++
		}
	}
	return count
}
