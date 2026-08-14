package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestEnrichContextStateSummarizesToolCallsDeepInConversation(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleUser, Content: "start"},
		{
			Role:    MessageRoleAssistant,
			Content: "searching",
			ToolCalls: []ToolCall{
				{ID: "call_grep", Name: "grep", Arguments: map[string]any{"pattern": "foo"}},
				{ID: "call_read", Name: "read", Arguments: map[string]any{"path": "file.go"}},
			},
		},
		{Role: MessageRoleTool, ToolCallID: "call_grep", Content: "matches"},
		{Role: MessageRoleTool, ToolCallID: "call_read", Content: "package main"},
	}
	// ~290 plain user/assistant pairs with no tool calls push the tool-call
	// messages far back; a fixed small tail would miss them.
	for i := 0; i < 290; i++ {
		messages = append(messages,
			Message{Role: MessageRoleUser, Content: fmt.Sprintf("user %d", i)},
			Message{Role: MessageRoleAssistant, Content: fmt.Sprintf("assistant %d", i)},
		)
	}

	state := RunState{TurnCount: 1, Lineage: newConversationLineage(messages)}
	got := (&ContextStateManager{}).enrichContextState(state)
	if len(got.RecentToolCalls) != 2 {
		t.Fatalf("RecentToolCalls = %v, want 2 summaries", got.RecentToolCalls)
	}
	joined := strings.Join(got.RecentToolCalls, " ")
	if !strings.Contains(joined, "grep") || !strings.Contains(joined, "read") {
		t.Fatalf("RecentToolCalls = %v, want summaries naming both grep and read", got.RecentToolCalls)
	}

	empty := RunState{Lineage: ConversationLineage{}}
	gotEmpty := (&ContextStateManager{}).enrichContextState(empty)
	if gotEmpty.RecentToolCalls != nil {
		t.Fatalf("empty lineage RecentToolCalls = %v, want nil", gotEmpty.RecentToolCalls)
	}
}

func TestEnrichContextStateSummarizesOnlyNewestGenerationToolCalls(t *testing.T) {
	gen1 := []Message{
		{
			Role:    MessageRoleAssistant,
			Content: "old search",
			ToolCalls: []ToolCall{
				{ID: "call_grep", Name: "grep", Arguments: map[string]any{"pattern": "foo"}},
			},
		},
	}
	summaryPrefix := []Message{{Role: MessageRoleSummary, Content: "compacted earlier content"}}
	gen2 := []Message{
		{Role: MessageRoleUser, Content: "continue"},
		{Role: MessageRoleAssistant, Content: "ok"},
		{
			Role:    MessageRoleAssistant,
			Content: "reading",
			ToolCalls: []ToolCall{
				{ID: "call_read", Name: "read", Arguments: map[string]any{"path": "file.go"}},
			},
		},
	}
	lineage := newConversationLineage(gen1).WithNewGeneration(summaryPrefix, gen2)

	latest := lineage.latestMessages()
	if got, want := len(latest), len(gen2); got != want {
		t.Fatalf("latestMessages() len = %d, want %d", got, want)
	}
	for i := range gen2 {
		if latest[i].Role != gen2[i].Role || latest[i].Content != gen2[i].Content {
			t.Fatalf("latestMessages()[%d] = %+v, want %+v", i, latest[i], gen2[i])
		}
	}
	if &latest[0] != &lineage.Generations[1].Messages[0] {
		t.Fatal("latestMessages() did not share backing with Generations[1].Messages; expected no clone")
	}

	state := RunState{TurnCount: 1, Lineage: lineage}
	recent := (&ContextStateManager{}).enrichContextState(state).RecentToolCalls
	if got, want := len(recent), 1; got != want {
		t.Fatalf("RecentToolCalls = %v, want %d summary", recent, want)
	}
	joined := strings.Join(recent, " ")
	if !strings.Contains(joined, "read") {
		t.Fatalf("RecentToolCalls = %v, want summary naming the newest read tool call", recent)
	}
	if strings.Contains(joined, "grep") {
		t.Fatalf("RecentToolCalls = %v, must not name grep from the older generation", recent)
	}
}
