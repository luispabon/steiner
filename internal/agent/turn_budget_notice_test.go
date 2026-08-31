package agent

import (
	"strings"
	"testing"
)

func countMarkedMessages(messages []Message) int {
	count := 0
	for _, m := range messages {
		if strings.HasPrefix(m.Content, turnBudgetNoticeMarker) {
			count++
		}
	}
	return count
}

func TestInjectTurnBudgetNoticeIfDue_FiresAtThreshold(t *testing.T) {
	req := RunRequest{
		Limits: Limits{MaxTurns: 10},
		TurnBudgetNotice: func(_, _ int) string {
			return "used it up"
		},
	}

	conversation := []Message{{Role: MessageRoleUser, Content: "hello"}}
	state := RunState{
		TurnCount:    6,
		Conversation: conversation,
		Lineage:      newConversationLineage(conversation),
	}

	before := injectTurnBudgetNoticeIfDue(state, req)
	if before.BudgetNoticeIssued {
		t.Fatalf("BudgetNoticeIssued = true before threshold (TurnCount=%d)", before.TurnCount)
	}
	if countMarkedMessages(before.Conversation) != 0 {
		t.Fatalf("expected no marked messages before threshold, got %d", countMarkedMessages(before.Conversation))
	}

	state.TurnCount = 7 // 70% of 10
	after := injectTurnBudgetNoticeIfDue(state, req)
	if !after.BudgetNoticeIssued {
		t.Fatal("expected BudgetNoticeIssued = true at threshold")
	}
	if countMarkedMessages(after.Conversation) != 1 {
		t.Fatalf("expected exactly 1 marked message at threshold, got %d", countMarkedMessages(after.Conversation))
	}
}

func TestInjectTurnBudgetNoticeIfDue_NilCallbackIsNoop(t *testing.T) {
	req := RunRequest{Limits: Limits{MaxTurns: 10}}
	conversation := []Message{{Role: MessageRoleUser, Content: "hello"}}
	state := RunState{
		TurnCount:    9,
		Conversation: conversation,
		Lineage:      newConversationLineage(conversation),
	}

	got := injectTurnBudgetNoticeIfDue(state, req)
	if got.BudgetNoticeIssued {
		t.Fatal("expected no-op when TurnBudgetNotice is nil")
	}
	if countMarkedMessages(got.Conversation) != 0 {
		t.Fatal("expected no marked messages when TurnBudgetNotice is nil")
	}
}

func TestInjectTurnBudgetNoticeIfDue_SecondCallSupersedesNotAppends(t *testing.T) {
	req := RunRequest{
		Limits: Limits{MaxTurns: 10},
		TurnBudgetNotice: func(_, _ int) string {
			return "checkpoint reached"
		},
	}

	conversation := []Message{{Role: MessageRoleUser, Content: "hello"}}
	state := RunState{
		TurnCount:    7,
		Conversation: conversation,
		Lineage:      newConversationLineage(conversation),
	}

	state = injectTurnBudgetNoticeIfDue(state, req)
	if countMarkedMessages(state.Conversation) != 1 {
		t.Fatalf("after first call: expected 1 marked message, got %d", countMarkedMessages(state.Conversation))
	}

	// Simulate a second turn past threshold within the same run — since
	// BudgetNoticeIssued is now true, this should stay a no-op...
	state.TurnCount = 8
	state = injectTurnBudgetNoticeIfDue(state, req)
	if countMarkedMessages(state.Conversation) != 1 {
		t.Fatalf("after second call with issued flag set: expected 1 marked message, got %d", countMarkedMessages(state.Conversation))
	}

	// ...but if the flag is reset (as happens across an extension boundary,
	// where a fresh RunState is created) and the marked message is still
	// present in the carried-over conversation, the notice supersedes it in
	// place rather than appending a second one.
	state.BudgetNoticeIssued = false
	state = injectTurnBudgetNoticeIfDue(state, req)
	if countMarkedMessages(state.Conversation) != 1 {
		t.Fatalf("after supersede: expected exactly 1 marked message, got %d", countMarkedMessages(state.Conversation))
	}
}

func TestInjectTurnBudgetNoticeIfDue_ZeroMaxTurnsIsNoop(t *testing.T) {
	req := RunRequest{
		Limits: Limits{MaxTurns: 0},
		TurnBudgetNotice: func(_, _ int) string {
			return "should not fire"
		},
	}
	conversation := []Message{{Role: MessageRoleUser, Content: "hello"}}
	state := RunState{
		TurnCount:    100,
		Conversation: conversation,
		Lineage:      newConversationLineage(conversation),
	}
	got := injectTurnBudgetNoticeIfDue(state, req)
	if got.BudgetNoticeIssued {
		t.Fatal("expected no-op when MaxTurns <= 0")
	}
}

func TestInjectTurnBudgetNoticeIfDue_SurvivesReplaySafeConversion(t *testing.T) {
	req := RunRequest{
		Limits: Limits{MaxTurns: 10},
		TurnBudgetNotice: func(_, _ int) string {
			return "used it up"
		},
	}

	// A realistic tail: assistant message with a pending tool call followed
	// by its tool result, mirroring where the checkpoint actually injects
	// (right after the previous turn's tool result was applied).
	conversation := []Message{
		{Role: MessageRoleUser, Content: "do the thing"},
		{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "read"}}},
		{Role: MessageRoleTool, ToolCallID: "c1", Content: "file contents"},
	}
	state := RunState{
		TurnCount:    7,
		Conversation: conversation,
		Lineage:      newConversationLineage(conversation),
	}

	state = injectTurnBudgetNoticeIfDue(state, req)
	if countMarkedMessages(state.Conversation) != 1 {
		t.Fatalf("expected exactly 1 marked message before conversion, got %d", countMarkedMessages(state.Conversation))
	}

	roundTripped := fromProviderMessages(ToReplaySafeProviderMessages(state.Conversation))
	if got := countMarkedMessages(roundTripped); got != 1 {
		t.Fatalf("marked messages after ReplaySafeConversation round trip = %d, want 1 (conversation = %+v)", got, roundTripped)
	}
}

func TestTurnBudgetNoticeMarker_SurvivesProviderMessageRoundTrip(t *testing.T) {
	marked := Message{
		Role:    MessageRoleUser,
		Content: turnBudgetNoticeMarker + " you have used 7 of 10 turns",
	}

	providerMsgs := ToProviderMessages([]Message{marked})
	roundTripped := fromProviderMessages(providerMsgs)

	if len(roundTripped) != 1 {
		t.Fatalf("round trip produced %d messages, want 1", len(roundTripped))
	}
	if !strings.HasPrefix(roundTripped[0].Content, turnBudgetNoticeMarker) {
		t.Fatalf("round-tripped content = %q, want prefix %q", roundTripped[0].Content, turnBudgetNoticeMarker)
	}
}
