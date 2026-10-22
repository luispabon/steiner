package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// TestRunnerRejectsMarkedToolCallAndRunsSibling checks that a malformed call
// gets an invalid_tool_input result while its valid sibling still executes, and
// that the turn continues to the model's next response.
func TestRunnerRejectsMarkedToolCallAndRunsSibling(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{
							ID:                  "call_bad",
							Name:                "read",
							Arguments:           map[string]any{},
							RawArguments:        "{}",
							ArgumentDecodeError: `decode "read" arguments: unexpected end of JSON input`,
						},
						{ID: "call_ok", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "done"},
				FinishReason: "stop",
			},
		},
	}

	var handlerInputs []map[string]any
	executor := tool.NewExecutor(tool.NewRegistry(tool.ToolDef{
		Name: "read",
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			handlerInputs = append(handlerInputs, input)
			return "hello", nil
		},
	}), config.Config{}, nil, t.TempDir(), "", tool.Unsandboxed{})

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider:      providerStub,
		Executor:      executor,
		Tools:         []provider.ToolSpec{{Type: "function", Function: provider.ToolFunctionSpec{Name: "read", Description: "Read files", Parameters: map[string]any{"type": "object"}}}},
		Prompt:        prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "read both"}}, ProjectContextBudgetBytes: 128},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		MaxTokens:     intPtr(64),
		Limits:        Limits{MaxTurns: 4, MaxTokens: 50},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 2; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
	if len(handlerInputs) != 1 || !strings.HasSuffix(fmt.Sprint(handlerInputs[0]["path"]), "note.txt") {
		t.Fatalf("handler inputs = %v, want one call with path ending note.txt", handlerInputs)
	}

	toolMsgs := toolMessages(providerStub.requests[1].Messages)
	if len(toolMsgs) != 2 {
		t.Fatalf("tool messages = %d, want 2", len(toolMsgs))
	}
	if toolMsgs[0].ToolCallID != "call_bad" || toolMsgs[1].ToolCallID != "call_ok" {
		t.Fatalf("tool call ids = %q, %q, want call_bad, call_ok", toolMsgs[0].ToolCallID, toolMsgs[1].ToolCallID)
	}
	if !strings.Contains(toolMsgs[0].Content, `"kind":"invalid_tool_input"`) {
		t.Errorf("malformed call result = %s, want invalid_tool_input envelope", toolMsgs[0].Content)
	}
	if !strings.Contains(toolMsgs[1].Content, "hello") {
		t.Errorf("valid call result = %s, want handler output", toolMsgs[1].Content)
	}
}
