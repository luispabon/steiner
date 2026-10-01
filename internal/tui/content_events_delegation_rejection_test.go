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
