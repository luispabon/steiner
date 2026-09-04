package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// TestRepro593_FollowUpAfterCancelledSiblingLandsInNewBox replays the exact
// sequence from the #593 screenshot: child-3 spawns, completes, gets a
// follow-up that completes; child-5 spawns and is cancelled mid-run
// (DelegationFailedEvent, emitted unscoped exactly as task.go does); the user
// then sends a follow_up to child-5. The scoped chunk that follows must land
// in the NEW follow-up box for child-5, not in child-3's box or child-5's own
// stale (cancelled) box.
func TestRepro593_FollowUpAfterCancelledSiblingLandsInNewBox(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
		showThinking:           true,
	}

	// 1. child-3 spawns and completes.
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_c3", map[string]any{"type": "explore", "task": "find files"}))
	buffer.AppendEvent(output.WithAgentScope(output.NewDelegationStartedEventWithType("child-3", "find files", "call_c3", "", "explore"), "child-3"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "child-3 initial output", output.ChunkSourceAssistant), "child-3"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{AgentID: "child-3", Status: "complete"}))

	// 2. child-3 gets a follow-up that completes.
	buffer.AppendEvent(output.NewToolCallStartedEvent(2, "follow_up", "call_fu3", map[string]any{"agent_id": "child-3", "message": "continue child-3"}))
	buffer.AppendEvent(output.WithAgentScope(output.NewDelegationStartedEventWithType("child-3", "continue child-3", "call_fu3", "", ""), "child-3"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(2, "child-3 follow-up output", output.ChunkSourceAssistant), "child-3"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{AgentID: "child-3", Status: "complete"}))

	// 3. child-5 spawns and is cancelled mid-run. task.go emits
	// DelegationFailedEvent via the raw (unscoped) events sink, so replay it
	// unscoped here too.
	buffer.AppendEvent(output.NewToolCallStartedEvent(3, "sub_agent", "call_c5", map[string]any{"type": "explore", "task": "find primes"}))
	buffer.AppendEvent(output.WithAgentScope(output.NewDelegationStartedEventWithType("child-5", "find primes", "call_c5", "", "explore"), "child-5"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(3, "child-5 partial output", output.ChunkSourceAssistant), "child-5"))
	buffer.AppendEvent(output.NewDelegationFailedEvent(output.DelegationFailedParams{AgentID: "child-5", Error: "cancelled"}))

	// Locate child-3's and child-5's (now-terminal) boxes before the follow-up.
	child3Box, ok := buffer.findDelegation("child-3")
	if !ok {
		t.Fatal("child-3 box not found after step 2")
	}
	child5StaleBox, ok := buffer.findDelegation("child-5")
	if !ok {
		t.Fatal("child-5 box not found after step 3")
	}

	// 4. User sends a follow_up to child-5.
	buffer.AppendEvent(output.NewToolCallStartedEvent(4, "follow_up", "call_fu5", map[string]any{"agent_id": "child-5", "message": "continue child-5"}))
	buffer.AppendEvent(output.WithAgentScope(output.NewDelegationStartedEventWithType("child-5", "continue child-5", "call_fu5", "", ""), "child-5"))
	buffer.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEventWithSource(4, "resuming child-5", output.ChunkSourceAssistant), "child-5"))

	newChild5Box, ok := buffer.activeDelegations["child-5"]
	if !ok {
		t.Fatal("child-5 not active after new follow_up's DelegationStarted")
	}

	if newChild5Box.dd == child5StaleBox.dd {
		t.Fatalf("new follow_up bound to the STALE cancelled child-5 box instead of opening a fresh one")
	}
	if newChild5Box.dd == child3Box.dd {
		t.Fatalf("new follow_up bound to child-3's box (the exact #593 symptom)")
	}

	if newChild5Box.dd.lastEntry() == nil || !strings.Contains(newChild5Box.dd.lastEntry().body, "resuming child-5") {
		t.Fatalf("new child-5 follow-up box did not receive its scoped chunk: %#v", newChild5Box.dd.entries)
	}
	if child3Box.dd.lastEntry() != nil && strings.Contains(child3Box.dd.lastEntry().body, "resuming child-5") {
		t.Fatalf("child-3 box wrongly received child-5's follow-up chunk: %#v", child3Box.dd.entries)
	}
	if child5StaleBox.dd.lastEntry() != nil && strings.Contains(child5StaleBox.dd.lastEntry().body, "resuming child-5") {
		t.Fatalf("stale cancelled child-5 box wrongly received the new follow-up's chunk: %#v", child5StaleBox.dd.entries)
	}
}
