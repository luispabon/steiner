package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestAsyncAckThenCompletionRendersOneCompletedSegment(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_a1", map[string]any{"type": "explore", "task": "find files"}))
	buffer.AppendEvent(output.WithAgentScope(output.NewDelegationStartedEventWithType("agent-a", "find files", "call_a1", "", "explore"), "agent-a"))
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "sub_agent", "call_a1", `{"output":"Sub-agent started.","status":"running","continuation":{"agent_id":"agent-a"}}`, nil))

	loc, ok := buffer.findDelegation("agent-a")
	if !ok {
		t.Fatal("delegation segment not found after ack")
	}
	if loc.dd.status != "active" {
		t.Fatalf("status after ack = %q, want active", loc.dd.status)
	}

	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{AgentID: "agent-a", Status: "complete", Output: "found it"}))

	count := 0
	buffer.forEachDelegationReverse(func(delegationLocator) bool {
		count++
		return false
	})
	if count != 1 {
		t.Fatalf("delegation segments = %d, want 1", count)
	}
	loc, _ = buffer.findDelegation("agent-a")
	if loc.dd.status != "complete" || loc.dd.output != "found it" {
		t.Errorf("delegation = status %q output %q, want complete/found it", loc.dd.status, loc.dd.output)
	}
}
