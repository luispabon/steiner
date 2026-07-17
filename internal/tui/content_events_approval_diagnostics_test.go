package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// TestApprovalCorrelatesByCallIDAcrossConcurrentCalls reproduces the bug where
// two concurrent calls to the same tool (e.g. two parallel bash calls) could
// have their approval state misattributed because correlation matched on tool
// name alone. Each call is tracked as an independent segmentToolCall entry
// (not merged into a group) so the segments are kept apart by an unrelated
// segment, mirroring how two non-adjacent tool calls would appear in the
// transcript.
func TestApprovalCorrelatesByCallIDAcrossConcurrentCalls(t *testing.T) {
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
