package tui

import (
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestDelegationRejectionRemovesOnlyCurrentUnacceptedCard(t *testing.T) {
	b := newGroupTestBuffer()
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "old-call", subAgentArgs("reused")))
	accepted := lastDelegationCard(b)
	occ := output.DelegationOccurrence{CallID: "old-call", BatchID: "batch", AgentID: "agent"}
	b.AppendEvent(output.NewDelegationAcceptedEvent(occ, "reused"))
	b.AppendEvent(output.NewDelegationStartedEvent(occ, "old task", "", "explore"))
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "current-call", subAgentArgs("reused")))
	provisional := lastDelegationCard(b)
	b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "after"))

	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "current-call", "", nil, output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))

	if findDelegationSegment(b.segments, accepted) < 0 || findDelegationSegment(b.segments, provisional) >= 0 {
		t.Fatalf("segments after rejection = %v", segmentKinds(b.segments))
	}
	if got := countDelegationCards(b.segments); got != 1 {
		t.Fatalf("delegation cards = %d, want only the accepted one", got)
	}
	if accepted.agentID != "agent" || accepted.parentCallID != "old-call" || accepted.status != "active" || b.activeDelegations["agent"].dd != accepted {
		t.Fatalf("accepted card changed: %#v", accepted)
	}
}

func TestDelegationRejectionDoesNotHideUnknownAdmissionError(t *testing.T) {
	const message = "api: legacy rejection-like error"
	b := &contentBuffer{segments: []contentSegment{{kind: segmentDelegation, delegData: &delegationDisplayState{parentCallID: "call"}}}}
	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "call", "", errors.New(message), output.ToolPreview{}, &output.DelegationAdmission{Status: "unknown"}))
	if len(b.segments) != 2 || b.segments[0].delegData == nil || b.segments[1].text != message {
		t.Fatalf("unknown admission card/error not retained: %#v", b.segments)
	}
}

func TestDelegationRejectionPreservesAcceptedAndUnknownCards(t *testing.T) {
	for _, admission := range []*output.DelegationAdmission{
		{Status: "accepted"},
		{Status: "unknown"},
		nil,
	} {
		b := newGroupTestBuffer()
		b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call", subAgentArgs("g")))
		dd := lastDelegationCard(b)
		if admission != nil && admission.Status == "accepted" {
			b.AppendEvent(output.NewDelegationAcceptedEvent(output.DelegationOccurrence{CallID: "call", BatchID: "batch"}, "g"))
		}
		b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "call", "result", nil, output.ToolPreview{}, admission))
		if findDelegationSegment(b.segments, dd) < 0 || countDelegationCards(b.segments) != 1 {
			t.Fatalf("admission %#v removed card: %v", admission, segmentKinds(b.segments))
		}
	}
}

func TestModelRejectedFollowUpPreservesOriginalChildAndRoster(t *testing.T) {
	m := &Model{content: contentBuffer{}, roster: subAgentRoster{entries: map[string]*rosterEntry{}}, styles: testStyles("#5599ff")}
	accepted := output.NewDelegationAcceptedEvent(output.DelegationOccurrence{CallID: "original", BatchID: "batch", AgentID: "child"}, "prior")
	m.applyEvent(accepted)
	m.applyEvent(output.NewDelegationQueuedEvent(output.DelegationOccurrence{CallID: "original", BatchID: "batch", AgentID: "child"}, "explore", "original task"))
	m.applyEvent(output.NewDelegationStartedEvent(output.DelegationOccurrence{CallID: "original", BatchID: "batch", AgentID: "child"}, "original task", "", "explore"))
	originalCard := m.content.segments[0].delegData
	before := m.roster.entries["child"]
	if originalCard == nil || before == nil || before.status != rosterRunning {
		t.Fatalf("original run setup: card=%#v roster=%#v", originalCard, before)
	}

	m.applyEvent(output.NewToolCallStartedEvent(1, "follow_up", "follow-up", map[string]any{"agent_id": "child", "message": "continue"}))
	followUp := m.content.segments[len(m.content.segments)-1].delegData
	if followUp == nil || !followUp.isFollowUp {
		t.Fatalf("follow-up provisional card = %#v", followUp)
	}
	m.applyEvent(output.NewToolCallFinishedEventWithAdmission(1, "follow_up", "follow-up", "", errors.New("not admitted"), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))

	if findDelegationSegment(m.content.segments, originalCard) < 0 || findDelegationSegment(m.content.segments, followUp) >= 0 {
		t.Fatalf("content cards after rejection = %#v", m.content.segments)
	}
	after := m.roster.entries["child"]
	if after != before || after.status != rosterRunning || after.group != "prior" || len(m.roster.entries) != 1 {
		t.Fatalf("roster changed for rejected follow-up: before=%#v after=%#v entries=%#v", before, after, m.roster.entries)
	}

	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: output.DelegationOccurrence{CallID: "original", BatchID: "batch", AgentID: "child"}, Status: "completed"}))
	if findDelegationSegment(m.content.segments, originalCard) < 0 || originalCard.agentID != "child" || originalCard.status != "complete" {
		t.Fatalf("late lifecycle event changed original card: %#v", originalCard)
	}
	after = m.roster.entries["child"]
	if after != before || after.status != rosterDone || after.group != "prior" || len(m.roster.entries) != 1 {
		t.Fatalf("late lifecycle event corrupted roster: %#v", m.roster.entries)
	}
}

func TestUnknownAdmissionErrorCleansEligibleEmptyAgentCard(t *testing.T) {
	const message = "turn /status: launch failed"
	dd := &delegationDisplayState{parentCallID: "call"}
	b := &contentBuffer{
		segments:          []contentSegment{{kind: segmentDelegation, delegData: dd}},
		openDelegations:   map[string]delegationLocator{"call": {seg: 0, dd: dd}},
		queuedDelegations: map[string]delegationLocator{"call": {seg: 0, dd: dd}},
	}

	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "call", "", errors.New(message), output.ToolPreview{}, &output.DelegationAdmission{Status: "unknown"}))

	if dd.status != "failed" || len(b.openDelegations) != 0 {
		t.Fatalf("eligible failure not cleaned: status=%q open=%#v", dd.status, b.openDelegations)
	}
	if _, ok := b.queuedDelegations["call"]; ok {
		t.Fatalf("queued call remains: %#v", b.queuedDelegations)
	}
	if len(b.segments) != 2 || b.segments[1].text != message {
		t.Fatalf("unknown admission evidence = %#v, want exact error once", b.segments)
	}
}

func TestUnknownAdmissionErrorKeepsIdentifiedAcceptedRunningCard(t *testing.T) {
	const message = "api: delayed error"
	b := newGroupTestBuffer()
	occ := output.DelegationOccurrence{CallID: "call", BatchID: "batch", AgentID: "child"}
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call", subAgentArgs("g")))
	dd := lastDelegationCard(b)
	b.AppendEvent(output.NewDelegationAcceptedEvent(occ, "g"))
	b.AppendEvent(output.NewDelegationStartedEvent(occ, "find files", "", "explore"))

	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "call", "", errors.New(message), output.ToolPreview{}, &output.DelegationAdmission{Status: "unknown"}))

	if dd.status != "active" || dd.agentID != "child" || findDelegationSegment(b.segments, dd) != 0 {
		t.Fatalf("identified accepted run changed: card=%#v segments=%#v", dd, b.segments)
	}
	if len(b.segments) != 2 || b.segments[1].text != message {
		t.Fatalf("unknown admission evidence = %#v, want exact error", b.segments)
	}
}

func TestDelegationRejectedErrorRetainsExactText(t *testing.T) {
	for _, message := range []string{"api: provider failed", "turn /status: failed", "status: rejected by policy", "ordinary rejection error"} {
		t.Run(message, func(t *testing.T) {
			b := &contentBuffer{segments: []contentSegment{{kind: segmentDelegation, delegData: &delegationDisplayState{parentCallID: "call"}}}}
			openParentCard(b, "call")
			b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "call", "", errors.New(message), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected", PolicyNotice: true}))
			if len(b.segments) != 2 || b.segments[0].kind != segmentStatus || b.segments[0].text != "Delegation rejected by policy." || b.segments[1].kind != segmentTool || b.segments[1].text != message {
				t.Fatalf("rejection evidence = %#v, want exact error %q", b.segments, message)
			}
		})
	}
}
