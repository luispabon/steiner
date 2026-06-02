package delegation

import (
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestFailedDelegateSummaryText_NoPreviousOutput(t *testing.T) {
	err := errors.New("deadline exceeded")
	summary := failedDelegateSummaryText(err, "")
	if !strings.Contains(summary, "delegation failed: deadline exceeded") {
		t.Fatalf("expected failure summary, got: %s", summary)
	}
}

func TestFailedDelegateSummaryText_OutputWins(t *testing.T) {
	err := errors.New("deadline exceeded")
	summary := failedDelegateSummaryText(err, "found 3 issues in pkg A")
	if !strings.Contains(summary, "previous output:") {
		t.Errorf("expected previous output in summary, got: %s", summary)
	}
}

func TestReplaySafeConversation_StripsDanglingToolCalls(t *testing.T) {
	conversation := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "investigate"},
		{
			Role:    agent.MessageRoleAssistant,
			Content: "starting search",
			ToolCalls: []agent.ToolCall{
				{ID: "call-1", Name: "grep", Arguments: map[string]any{"pattern": "foo"}},
			},
		},
		{Role: agent.MessageRoleAssistant, Content: "interrupted"},
		{Role: agent.MessageRoleTool, Content: "orphaned result", ToolCallID: "orphan"},
	}

	got := replaySafeConversation(conversation)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	if len(got[1].ToolCalls) != 0 {
		t.Fatalf("got[1].ToolCalls = %#v, want cleared dangling tool calls", got[1].ToolCalls)
	}
	if got[2].Role != agent.MessageRoleAssistant || got[2].Content != "interrupted" {
		t.Fatalf("got[2] = %#v, want trailing assistant preserved", got[2])
	}
}

func TestReplaySafeConversation_PreservesPairedToolResults(t *testing.T) {
	conversation := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "investigate"},
		{
			Role:    agent.MessageRoleAssistant,
			Content: "running search",
			ToolCalls: []agent.ToolCall{
				{ID: "call-1", Name: "grep", Arguments: map[string]any{"pattern": "foo"}},
			},
		},
		{Role: agent.MessageRoleTool, Content: "match", ToolCallID: "call-1"},
		{Role: agent.MessageRoleAssistant, Content: "done"},
	}

	got := replaySafeConversation(conversation)
	if len(got) != len(conversation) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(conversation))
	}
	if len(got[1].ToolCalls) != 1 {
		t.Fatalf("got[1].ToolCalls = %#v, want paired tool call preserved", got[1].ToolCalls)
	}
	if got[2].Role != agent.MessageRoleTool || got[2].ToolCallID != "call-1" {
		t.Fatalf("got[2] = %#v, want paired tool result preserved", got[2])
	}
}
