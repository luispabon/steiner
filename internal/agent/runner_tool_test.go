package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

//nolint:gocyclo
func TestRunnerExecutesToolThenFinalAnswer(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{
							ID:   "call_1",
							Name: "read",
							Arguments: map[string]any{
								"path": "note.txt",
							},
						},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 7, CompletionTokens: 7},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 3, CompletionTokens: 3},
			},
		},
	}

	executor := &fakeExecutor{
		execute: func(_ context.Context, toolName string, input map[string]any) (any, error) {
			if toolName != "read" {
				return nil, fmt.Errorf("tool = %s, want read", toolName)
			}
			if got := input["path"]; got != "note.txt" {
				return nil, fmt.Errorf("input[path] = %v, want note.txt", got)
			}
			return tool.ExecutionResult{
				Value: map[string]any{"contents": "hello"},
				Metadata: tool.ExecutionMetadata{
					ExitCode: 0,
				},
			}, nil
		},
	}

	var events []output.Event
	runner := NewRunner()
	state, err := runner.Run(context.Background(), RunRequest{
		Provider:      providerStub,
		Executor:      executor,
		Tools:         []provider.ToolSpec{{Type: "function", Function: provider.ToolFunctionSpec{Name: "read", Description: "Read files", Parameters: map[string]any{"type": "object"}}}},
		Prompt:        prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "fix the bug"}}, ProjectContextBudgetBytes: 128},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		MaxTokens:     intPtr(64),
		Limits:        Limits{MaxTurns: 4, MaxTokens: 50},
		Events:        output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := state.TurnCount, 2; got != want {
		t.Fatalf("TurnCount = %d, want %d", got, want)
	}
	if got, want := state.TokenCount, 10; got != want {
		t.Fatalf("TokenCount = %d, want %d", got, want)
	}
	if got, want := len(providerStub.requests), 2; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
	if got, want := len(providerStub.requests[0].Tools), 1; got != want {
		t.Fatalf("first request tools = %d, want %d", got, want)
	}
	if got, want := providerStub.requests[0].Tools[0].Function.Name, "read"; got != want {
		t.Fatalf("first request tool name = %q, want %q", got, want)
	}
	if got, want := len(executor.calls), 1; got != want {
		t.Fatalf("executor calls = %d, want %d", got, want)
	}

	second := providerStub.requests[1]
	if got := rolesOf(second.Messages); !containsSequence(got, []string{"system", "user", "assistant", "tool"}) {
		t.Fatalf("second request roles = %v, want system/user/assistant/tool", got)
	}
	secondTools := toolMessages(second.Messages)
	if len(secondTools) == 0 {
		t.Fatalf("second request tool messages = %d, want at least 1", len(secondTools))
	}
	if got := secondTools[len(secondTools)-1].ToolCallID; got != "call_1" {
		t.Fatalf("tool call id = %q, want call_1", got)
	}
	if got, want := secondTools[len(secondTools)-1].Content, `{"contents":"hello"}`; got != want {
		t.Fatalf("tool result content = %q, want %q", got, want)
	}

	wantEventTypes := []string{
		output.EventTypeContextDiagnostics,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeContextDiagnostics,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeStopReason,
	}
	if got := eventTypes(events); !equalStrings(got, wantEventTypes) {
		t.Fatalf("event types = %v, want %v", got, wantEventTypes)
	}
}

//nolint:gocyclo
func TestRunnerContextStateManagerShapesFreshToolResultsOnAppend(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{
							ID:   "call_1",
							Name: "bash",
							Arguments: map[string]any{
								"command": "echo test",
							},
						},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 7, CompletionTokens: 7},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 3, CompletionTokens: 3},
			},
		},
	}

	executor := &fakeExecutor{
		execute: func(_ context.Context, toolName string, _ map[string]any) (any, error) {
			if toolName != "bash" {
				return nil, fmt.Errorf("tool = %s, want bash", toolName)
			}
			return tool.ExecutionResult{
				Value: map[string]any{
					"exit_code": 1,
					"output":    "HEAD-SENTINEL\n" + strings.Repeat("filler line\n", 1200) + "\x1b[31mwarning: retry\x1b[0m\nwarning: retry\nwarning: retry\nfinal tail\n",
				},
				Metadata: tool.ExecutionMetadata{
					ExitCode: 1,
				},
			}, nil
		},
	}

	runner := NewRunner()
	state, err := runner.Run(context.Background(), RunRequest{
		Provider:       providerStub,
		Executor:       executor,
		ContextManager: &ContextStateManager{},
		Tools:          []provider.ToolSpec{{Type: "function", Function: provider.ToolFunctionSpec{Name: "bash", Description: "Run shell commands", Parameters: map[string]any{"type": "object"}}}},
		Prompt:         prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "fix the bug"}}, ProjectContextBudgetBytes: 128},
		ResolvedModel:  provider.ResolvedModel{BackendModelID: "test-model"},
		MaxTokens:      intPtr(64),
		Limits:         Limits{MaxTurns: 4, MaxTokens: 50},
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

	second := providerStub.requests[1]
	var toolResultMsg provider.Message
	for _, m := range second.Messages {
		if m.ToolCallID == "call_1" {
			toolResultMsg = m
			break
		}
	}
	if toolResultMsg.ToolCallID != "call_1" {
		t.Fatalf("tool call id = %q, want call_1", toolResultMsg.ToolCallID)
	}
	var toolResult struct {
		Output    string `json:"output"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(toolResultMsg.Content), &toolResult); err != nil {
		t.Fatalf("unmarshal tool result: %v", err)
	}
	if strings.Contains(toolResult.Output, "HEAD-SENTINEL") {
		t.Fatalf("tool result output = %q, want head truncated at append time", toolResult.Output)
	}
	if strings.Contains(toolResult.Output, "\x1b[") {
		t.Fatalf("tool result output = %q, want ANSI stripped", toolResult.Output)
	}
	if !strings.Contains(toolResult.Output, "warning: retry (repeated 3x)") {
		t.Fatalf("tool result output = %q, want repeated warning collapse", toolResult.Output)
	}
	if !toolResult.Truncated {
		t.Fatal("tool result truncated = false, want true")
	}
	if !strings.Contains(toolResult.Output, "<truncated output shown=") {
		t.Fatalf("tool result output = %q, want truncation marker", toolResult.Output)
	}
}

func TestRunnerTreatsToolContextCancellationAsCancelled(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(ctx context.Context, _ string, _ map[string]any) (any, error) {
			cancelFunc := ctx.Value(cancelContextKey{}).(context.CancelFunc)
			cancelFunc()
			return nil, ctx.Err()
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, cancelContextKey{}, cancel)

	var events []output.Event
	state, err := NewRunner().Run(ctx, RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "fix the bug"}},
		},
		Limits: Limits{MaxTurns: 2, MaxTokens: 10},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if got, want := state.StopReason, StopReasonCancelled; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if len(state.Conversation) != 3 {
		t.Fatalf("len(state.Conversation) = %d, want user, assistant, and tool messages", len(state.Conversation))
	}
	if got := state.Conversation[0]; got.Role != MessageRoleUser || got.Content != "fix the bug" {
		t.Fatalf("state.Conversation[0] = %#v, want original user message", got)
	}
	if got := state.Conversation[2]; got.Role != MessageRoleTool || got.ToolCallID != "call_1" {
		t.Fatalf("state.Conversation[2] = %#v, want paired tool result", got)
	}
	if got, want := eventTypes(events), []string{
		output.EventTypeContextDiagnostics,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeStopReason,
	}; !equalStrings(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
}

func TestRunnerReplaysCancelledToolCallTranscriptSafelyOnNextPrompt(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "all set",
				},
				FinishReason: "stop",
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(ctx context.Context, _ string, _ map[string]any) (any, error) {
			cancelFunc := ctx.Value(cancelContextKey{}).(context.CancelFunc)
			cancelFunc()
			return nil, ctx.Err()
		},
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx1 = context.WithValue(ctx1, cancelContextKey{}, cancel1)

	firstState, err := NewRunner().Run(ctx1, RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "fix the bug"}},
		},
		Limits: Limits{MaxTurns: 2, MaxTokens: 10},
	})
	if err != nil {
		t.Fatalf("first Run() error = %v, want nil", err)
	}
	if got, want := firstState.StopReason, StopReasonCancelled; got != want {
		t.Fatalf("first StopReason = %q, want %q", got, want)
	}

	secondConversation := append(cloneMessages(firstState.Conversation), Message{
		Role: MessageRoleUser, Content: "continue",
	})
	secondState, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{},
		Prompt: prompt.AssemblyOptions{
			Conversation: ToProviderMessages(secondConversation),
		},
		Limits: Limits{MaxTurns: 3, MaxTokens: 10},
	})
	if err != nil {
		t.Fatalf("second Run() error = %v, want nil", err)
	}
	if got, want := secondState.StopReason, StopReasonComplete; got != want {
		t.Fatalf("second StopReason = %q, want %q", got, want)
	}
	if len(providerStub.requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(providerStub.requests))
	}
	replay := providerStub.requests[1].Messages
	var foundDanglingAssistant bool
	for _, message := range replay {
		if message.Role == provider.MessageRoleTool && message.ToolCallID != "call_1" {
			t.Fatalf("replay messages = %#v, found unexpected tool result", replay)
		}
		if message.Role == provider.MessageRoleAssistant && strings.TrimSpace(message.Content) == "" && len(message.ToolCalls) == 0 {
			t.Fatalf("replay messages = %#v, want empty assistant messages dropped", replay)
		}
		if message.Role == provider.MessageRoleAssistant && len(message.ToolCalls) > 0 {
			foundDanglingAssistant = true
		}
	}
	if !foundDanglingAssistant {
		t.Fatalf("replay messages = %#v, want paired assistant tool call", replay)
	}
	last := replay[len(replay)-1]
	if last.Role != provider.MessageRoleUser || last.Content != "continue" {
		t.Fatalf("replay tail = %#v, want follow-up user message", last)
	}
}

func TestRunnerUsesExecutionResultWithoutLeakingMetadata(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
			},
		},
	}

	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return tool.ExecutionResult{
				Value: map[string]any{"contents": "hello"},
				Metadata: tool.ExecutionMetadata{
					ExitCode: 0,
					Stdout: tool.StreamCapture{
						Bytes:     1024,
						Truncated: true,
						Preview:   strings.Repeat("hello", 4),
					},
				},
				Retention: &tool.ToolRetention{
					Kind:       tool.RetentionKindDelegateSummary,
					Summary:    "child summary",
					AgentID:    "child-1",
					Status:     "complete",
					TurnCount:  1,
					TokenCount: 4,
				},
			}, nil
		},
	}

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "fix"}},
		},
		Limits: Limits{MaxTurns: 3, MaxTokens: 1000},
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
	secondTools := toolMessages(providerStub.requests[1].Messages)
	if len(secondTools) == 0 {
		t.Fatalf("second request tool messages = %d, want at least 1", len(secondTools))
	}
	if got, want := secondTools[len(secondTools)-1].Content, `{"contents":"hello"}`; got != want {
		t.Fatalf("tool message content = %q, want %q", got, want)
	}
	if strings.Contains(secondTools[len(secondTools)-1].Content, "child summary") {
		t.Fatal("tool message content leaked retention summary")
	}
	if got := state.Conversation[2].Retention; got == nil {
		t.Fatal("tool message retention = nil, want durable retained summary")
	} else if got.Summary != "child summary" {
		t.Fatalf("tool message retention summary = %q, want child summary", got.Summary)
	}
}

func TestRunnerKeepsRecentDelegateRetentionVisibleWithoutLeakingSummary(t *testing.T) {
	const fullOutput = "delegate produced a full result with paths and details"
	const hiddenSummary = "hidden summary marker"

	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_delegate", Name: "delegate", Arguments: map[string]any{"task": "inspect the repository and summarize the findings"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
			},
		},
	}

	executor := &fakeExecutor{
		execute: func(context.Context, string, map[string]any) (any, error) {
			return tool.ExecutionResult{
				Value: map[string]any{
					"output": fullOutput,
				},
				Retention: &tool.ToolRetention{
					Kind:       tool.RetentionKindDelegateSummary,
					Summary:    hiddenSummary,
					AgentID:    "child-1",
					Status:     "complete",
					TurnCount:  1,
					TokenCount: 8,
				},
			}, nil
		},
	}

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Tools: []provider.ToolSpec{
			{
				Type: "function",
				Function: provider.ToolFunctionSpec{
					Name:        "delegate",
					Description: "Delegate work",
					Parameters:  map[string]any{"type": "object"},
				},
			},
		},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "delegate this task"}},
		},
		Limits: Limits{MaxTurns: 3, MaxTokens: 1000},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := len(providerStub.requests), 2; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
	if got := state.Conversation[2].Retention; got == nil {
		t.Fatal("tool message retention = nil, want durable retained summary")
	} else if got.Summary != hiddenSummary {
		t.Fatalf("tool message retention summary = %q, want %q", got.Summary, hiddenSummary)
	}

	second := providerStub.requests[1]
	if !messageContentsContain(second.Messages, fullOutput) {
		t.Fatalf("second request missing full delegate output: %#v", second.Messages)
	}
	if messageContentsContain(second.Messages, hiddenSummary) {
		t.Fatalf("second request leaked retained summary: %#v", second.Messages)
	}
}

func TestRunnerKeepsDisplayFileResultMetadataOnly(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "display_file", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
			},
		},
	}

	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return &builtin.DisplayFileResult{
				Path:   "note.txt",
				Status: "displayed",
			}, nil
		},
	}

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "show note"}},
		},
		Limits: Limits{MaxTurns: 3, MaxTokens: 1000},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	secondTools := toolMessages(providerStub.requests[1].Messages)
	if len(secondTools) == 0 {
		t.Fatalf("second request tool messages = %d, want at least 1", len(secondTools))
	}
	if got, want := secondTools[len(secondTools)-1].Content, `{"path":"note.txt","status":"displayed"}`; got != want {
		t.Fatalf("tool message content = %q, want %q", got, want)
	}
}

//nolint:gocyclo
func TestRunnerExecutesMultipleToolCallsSequentially(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a"}},
						{ID: "call_2", Name: "glob", Arguments: map[string]any{"pattern": "*.go"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
			},
		},
	}
	executor := &fakeExecutor{execute: func(_ context.Context, toolName string, input map[string]any) (any, error) {
		switch toolName {
		case "read":
			return map[string]any{"path": input["path"], "contents": "alpha"}, nil
		case "glob":
			return map[string]any{"pattern": input["pattern"], "matches": []string{"main.go"}}, nil
		default:
			return nil, fmt.Errorf("unexpected tool %q", toolName)
		}
	}}

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		Limits: Limits{MaxTurns: 2, MaxTokens: 1000},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(executor.calls), 2; got != want {
		t.Fatalf("executor calls = %d, want %d", got, want)
	}
	if got, want := executor.calls[0].tool, "read"; got != want {
		t.Fatalf("first tool = %q, want %q", got, want)
	}
	if got, want := executor.calls[1].tool, "glob"; got != want {
		t.Fatalf("second tool = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 2; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
	second := providerStub.requests[1]
	if got := rolesOf(second.Messages); !containsSequence(got, []string{"assistant", "tool", "tool"}) {
		t.Fatalf("second request roles = %v, want assistant/tool/tool sequence", got)
	}
	secondTools := toolMessages(second.Messages)
	if len(secondTools) != 2 {
		t.Fatalf("second request tool messages = %d, want 2", len(secondTools))
	}
	if got, want := secondTools[0].ToolCallID, "call_1"; got != want {
		t.Fatalf("first tool call id = %q, want %q", got, want)
	}
	if got, want := secondTools[1].ToolCallID, "call_2"; got != want {
		t.Fatalf("second tool call id = %q, want %q", got, want)
	}

	wantEventTypes := []string{
		output.EventTypeContextDiagnostics,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeContextDiagnostics,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeStopReason,
	}
	if got := eventTypes(events); !equalStrings(got, wantEventTypes) {
		t.Fatalf("event types = %v, want %v", got, wantEventTypes)
	}
}

func TestRunnerContextStateManagerSanitizesRecentToolCallSummaries(t *testing.T) {
	cwd := t.TempDir()

	cm := &ContextStateManager{}
	state := RunState{
		TurnCount: 1,
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: "run tests", Turn: 1},
			{Role: MessageRoleAssistant, Content: "running tests", Turn: 1, ToolCalls: []ToolCall{
				{
					ID:   "call_1",
					Name: "bash",
					Arguments: map[string]any{
						"cwd":     cwd,
						"command": "cd " + cwd + " && go test ./internal/agent",
					},
				},
			}},
		}),
	}

	got, err := cm.PrepareTurnState(context.Background(), state)
	if err != nil {
		t.Fatalf("PrepareTurnState() error = %v", err)
	}
	if len(got.Context.RecentToolCalls) != 1 {
		t.Fatalf("recent tool calls = %v, want 1 summary", got.Context.RecentToolCalls)
	}
	if strings.Contains(got.Context.RecentToolCalls[0], cwd) {
		t.Fatalf("recent tool call summary = %q, want no absolute cwd", got.Context.RecentToolCalls[0])
	}
	if !strings.Contains(got.Context.RecentToolCalls[0], "go test ./internal/agent") {
		t.Fatalf("recent tool call summary = %q, want sanitized command fragment", got.Context.RecentToolCalls[0])
	}
}
