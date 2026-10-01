package tui

import (
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

var errTestDelegationRejected = errors.New("blocked by policy")

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

func TestDelegationRejectedPolicyAndErrorRemainVisible(t *testing.T) {
	b := &contentBuffer{segments: []contentSegment{{kind: segmentDelegation, delegData: &delegationDisplayState{parentCallID: "call"}}}}
	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "call", "", errTestDelegationRejected, output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected", PolicyNotice: true}))
	if len(b.segments) != 2 || b.segments[0].kind != segmentStatus || b.segments[0].text != "Delegation rejected by policy." || b.segments[1].text == "" {
		t.Fatalf("rejection evidence not retained: %#v", b.segments)
	}
}
