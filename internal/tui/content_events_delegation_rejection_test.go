package tui

import (
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestDelegationRejectionRemovesOnlyCurrentUnacceptedCard(t *testing.T) {
	accepted := &delegationDisplayState{parentCallID: "old-call", agentID: "agent", groupAccepted: true, batchID: "batch", group: "reused"}
	provisional := &delegationDisplayState{parentCallID: "current-call", agentID: "agent", group: "reused"}
	b := &contentBuffer{segments: []contentSegment{
		{kind: segmentDelegation, delegData: accepted},
		{kind: segmentDelegation, delegData: provisional},
		{kind: segmentPlain, text: "after"},
	}}

	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "current-call", "", nil, output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))

	if len(b.segments) != 2 || b.segments[0].delegData != accepted || b.segments[1].text != "after" {
		t.Fatalf("segments after rejection = %#v", b.segments)
	}
	if b.structureGen != 1 {
		t.Fatalf("structure generation = %d, want 1", b.structureGen)
	}
	if b.segments[0].delegData != accepted {
		t.Fatal("original accepted card changed or disappeared")
	}
	b.appendDelegationEvent(output.NewDelegationStartedEvent("agent", "old task", "old-call"))
	if accepted.agentID != "agent" || accepted.parentCallID != "old-call" || len(b.segments) != 3 || b.segments[2].delegData == accepted {
		t.Fatalf("late event rebound removed current call: accepted=%#v segments=%#v", accepted, b.segments)
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
		dd := &delegationDisplayState{parentCallID: "call", status: "failed", errMsg: "failed", groupAccepted: admission != nil && admission.Status == "accepted"}
		b := &contentBuffer{segments: []contentSegment{{kind: segmentDelegation, delegData: dd}}}
		b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "call", "result", nil, output.ToolPreview{}, admission))
		if len(b.segments) != 1 || b.segments[0].delegData != dd {
			t.Fatalf("admission %#v removed card: %#v", admission, b.segments)
		}
	}
}

func TestModelRejectedFollowUpPreservesOriginalChildAndRoster(t *testing.T) {
	m := &Model{content: contentBuffer{}, roster: subAgentRoster{entries: map[string]*rosterEntry{}}, styles: testStyles("#5599ff")}
	accepted := output.NewDelegationAcceptedEvent("original", "child", "batch", "prior")
	m.applyEvent(accepted)
	m.applyEvent(output.NewDelegationQueuedEvent("child", "original", "explore", "original task"))
	m.applyEvent(output.NewDelegationStartedEventWithType("child", "original task", "original", "", "explore"))
	originalCard := m.content.segments[0].delegData
	before := m.roster.entries["child"]
	if originalCard == nil || before == nil || before.status != rosterRunning || before.currentCallID != "original" {
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
	if after != before || after.currentCallID != "original" || after.status != rosterRunning || !after.accepted || after.group != "prior" || after.batchID != "batch" || len(m.roster.entries) != 1 {
		t.Fatalf("roster changed for rejected follow-up: before=%#v after=%#v entries=%#v", before, after, m.roster.entries)
	}

	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{AgentID: "child", Status: "completed"}))
	if findDelegationSegment(m.content.segments, originalCard) < 0 || originalCard.agentID != "child" || originalCard.status != "complete" {
		t.Fatalf("late lifecycle event changed original card: %#v", originalCard)
	}
	after = m.roster.entries["child"]
	if after != before || after.currentCallID != "original" || after.status != rosterDone || after.group != "prior" || len(m.roster.entries) != 1 {
		t.Fatalf("late lifecycle event corrupted roster: %#v", m.roster.entries)
	}
}

func TestDelegationRejectedErrorRetainsExactText(t *testing.T) {
	for _, message := range []string{"api: provider failed", "turn /status: failed", "status: rejected by policy", "ordinary rejection error"} {
		t.Run(message, func(t *testing.T) {
			b := &contentBuffer{segments: []contentSegment{{kind: segmentDelegation, delegData: &delegationDisplayState{parentCallID: "call"}}}}
			b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "call", "", errors.New(message), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected", PolicyNotice: true}))
			if len(b.segments) != 2 || b.segments[0].kind != segmentStatus || b.segments[0].text != "Delegation rejected by policy." || b.segments[1].kind != segmentTool || b.segments[1].text != message {
				t.Fatalf("rejection evidence = %#v, want exact error %q", b.segments, message)
			}
		})
	}
}
