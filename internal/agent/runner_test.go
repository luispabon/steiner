package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

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
				Usage:        &provider.UsageStats{TotalTokens: 7},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 3},
			},
		},
	}

	executor := &fakeExecutor{
		execute: func(ctx context.Context, toolName string, input map[string]any) (any, error) {
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
		Provider:  providerStub,
		Executor:  executor,
		Tools:     []provider.ToolSpec{{Type: "function", Function: provider.ToolFunctionSpec{Name: "read", Description: "Read files", Parameters: map[string]any{"type": "object"}}}},
		Prompt:    prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "fix the bug"}}, ProjectContextBudgetBytes: 128},
		Model:     "test-model",
		MaxTokens: intPtr(64),
		Limits:    Limits{MaxTurns: 4, MaxTokens: 50},
		Events:    output.SinkFunc(func(event output.Event) { events = append(events, event) }),
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
	if got := second.Messages[len(second.Messages)-1].ToolCallID; got != "call_1" {
		t.Fatalf("tool call id = %q, want call_1", got)
	}
	if got, want := second.Messages[len(second.Messages)-1].Content, `{"contents":"hello"}`; got != want {
		t.Fatalf("tool result content = %q, want %q", got, want)
	}

	wantEventTypes := []string{
		output.EventTypeContextDiagnostics,
		output.EventTypeContextDiagnostics,
		output.EventTypeTurnStarted,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeTurnFinished,
		output.EventTypeContextDiagnostics,
		output.EventTypeContextDiagnostics,
		output.EventTypeTurnStarted,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeTurnFinished,
		output.EventTypeStopReason,
	}
	if got := eventTypes(events); !equalStrings(got, wantEventTypes) {
		t.Fatalf("event types = %v, want %v", got, wantEventTypes)
	}
}

func TestRunnerPreservesToolResultContentWhileEmittingInternalPreview(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})

	target := filepath.Join(dir, "created.txt")
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{
							ID:   "call_1",
							Name: "write",
						Arguments: map[string]any{
							"path":    target,
							"content": "hello\n",
						},
						},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 7},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 3},
			},
		},
	}

	executor := &fakeExecutor{
		execute: func(ctx context.Context, toolName string, input map[string]any) (any, error) {
			return tool.ExecutionResult{
				Value: map[string]any{
					"path":          input["path"],
					"bytes_written": len(input["content"].(string)),
				},
			}, nil
		},
	}

	var events []output.Event
	_, err = NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "write file"}},
		},
		Model:  "test-model",
		Limits: Limits{MaxTurns: 2, MaxTokens: 50},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	second := providerStub.requests[1]
	if got, want := second.Messages[len(second.Messages)-1].Content, fmt.Sprintf(`{"bytes_written":6,"path":"%s"}`, target); got != want {
		t.Fatalf("tool result content = %q, want %q", got, want)
	}

	var started output.ToolCallStartedEvent
	var finished output.ToolCallFinishedEvent
	for _, event := range events {
		switch payload := event.Payload.(type) {
		case output.ToolCallStartedEvent:
			started = payload
		case output.ToolCallFinishedEvent:
			finished = payload
		}
	}

	if started.WriteTargetExistedBefore == nil || *started.WriteTargetExistedBefore {
		t.Fatalf("WriteTargetExistedBefore = %v, want false", started.WriteTargetExistedBefore)
	}
	if got, want := finished.Result, fmt.Sprintf(`{"bytes_written":6,"path":"%s"}`, target); got != want {
		t.Fatalf("finished result = %q, want %q", got, want)
	}
	if got, want := finished.Preview.Kind, output.ToolPreviewKindFileWrite; got != want {
		t.Fatalf("preview kind = %q, want %q", got, want)
	}
	if got, want := finished.Preview.Path, target; got != want {
		t.Fatalf("preview path = %q, want %q", got, want)
	}
	if got, want := finished.Preview.Contents, "hello\n"; got != want {
		t.Fatalf("preview contents = %q, want %q", got, want)
	}
	if !finished.Preview.Created {
		t.Fatalf("preview created = false, want true")
	}
}

func TestRunnerStreamsAssistantChunksBeforeFinalMessage(t *testing.T) {
	providerStub := &fakeProvider{
		streamFn: func(ctx context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
			chunks := make(chan provider.ChatChunk, 2)
			go func() {
				defer close(chunks)
				chunks <- provider.ChatChunk{
					Delta: provider.Message{
						Role:    provider.MessageRoleAssistant,
						Content: "hel",
					},
				}
				chunks <- provider.ChatChunk{
					Delta: provider.Message{
						Content: "lo",
					},
					Done:         true,
					FinishReason: "stop",
					Usage:        &provider.UsageStats{TotalTokens: 2},
				}
			}()
			return chunks, nil
		},
	}

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		Limits: Limits{MaxTurns: 2, MaxTokens: 10},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := state.TurnCount, 1; got != want {
		t.Fatalf("TurnCount = %d, want %d", got, want)
	}
	if got, want := state.Conversation[len(state.Conversation)-1].Content, "hello"; got != want {
		t.Fatalf("assistant content = %q, want %q", got, want)
	}
	wantTypes := []string{
		output.EventTypeContextDiagnostics,
		output.EventTypeContextDiagnostics,
		output.EventTypeTurnStarted,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAssistantChunk,
		output.EventTypeAssistantChunk,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeTurnFinished,
		output.EventTypeStopReason,
	}
	if got := eventTypes(events); !equalStrings(got, wantTypes) {
		t.Fatalf("event types = %v, want %v", got, wantTypes)
	}
}

func TestRunnerFallsBackToEstimatorWhenUsageIsMissing(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
			},
		},
	}

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "estimate the fallback"},
			},
			ProjectContextBudgetBytes: 128,
		},
		Model:     "test-model",
		MaxTokens: intPtr(32),
		Limits:    Limits{MaxTurns: 2, MaxTokens: 100},
		Events:    output.NoopSink{},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 1; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}

	estimated, err := provider.EstimateChatRequestTokens(context.Background(), providerStub.requests[0])
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	if got, want := state.TokenCount, estimated; got != want {
		t.Fatalf("TokenCount = %d, want %d", got, want)
	}
}

func TestRunnerPrefersReportedUsageOverFallbackEstimate(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 1},
			},
		},
	}

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "prefer usage when it is reported"},
			},
			ProjectContextBudgetBytes: 128,
		},
		Model:     "test-model",
		MaxTokens: intPtr(32),
		Limits:    Limits{MaxTurns: 2, MaxTokens: 100},
		Events:    output.NoopSink{},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	estimated, err := provider.EstimateChatRequestTokens(context.Background(), providerStub.requests[0])
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	if got, want := state.TokenCount, 1; got != want {
		t.Fatalf("TokenCount = %d, want %d", got, want)
	}
	if got := state.TokenCount; got == estimated {
		t.Fatalf("TokenCount = %d, want usage to override fallback estimate %d", got, estimated)
	}
}

func TestRunnerStopsAtMaxTurns(t *testing.T) {
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
		execute: func(ctx context.Context, toolName string, input map[string]any) (any, error) {
			return map[string]any{"contents": "hello"}, nil
		},
	}

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "fix the bug"}},
		},
		Limits: Limits{MaxTurns: 1, MaxTokens: 10},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonMaxTurns; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := state.TurnCount, 1; got != want {
		t.Fatalf("TurnCount = %d, want %d", got, want)
	}
	if got, want := eventTypes(events)[len(events)-1], output.EventTypeStopReason; got != want {
		t.Fatalf("last event type = %q, want %q", got, want)
	}
}

func TestRunnerTreatsProviderContextCancellationAsCancelled(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
				},
			},
		},
	}
	providerStub.chatFn = func(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
		<-ctx.Done()
		return provider.ChatResponse{}, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var events []output.Event
	state, err := NewRunner().Run(ctx, RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{},
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
	if got, want := eventTypes(events), []string{output.EventTypeStopReason}; !equalStrings(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
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
		execute: func(ctx context.Context, toolName string, input map[string]any) (any, error) {
			cancelFunc := ctx.Value(cancelContextKey{}).(context.CancelFunc)
			cancelFunc()
			return nil, ctx.Err()
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, cancelContextKey{}, context.CancelFunc(cancel))

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
	if got, want := eventTypes(events), []string{
		output.EventTypeContextDiagnostics,
		output.EventTypeContextDiagnostics,
		output.EventTypeTurnStarted,
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
		execute: func(ctx context.Context, toolName string, input map[string]any) (any, error) {
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
	if got, want := providerStub.requests[1].Messages[len(providerStub.requests[1].Messages)-1].Content, `{"contents":"hello"}`; got != want {
		t.Fatalf("tool message content = %q, want %q", got, want)
	}
}

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
	executor := &fakeExecutor{execute: func(ctx context.Context, toolName string, input map[string]any) (any, error) {
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
	if got, want := second.Messages[len(second.Messages)-2].ToolCallID, "call_1"; got != want {
		t.Fatalf("first tool call id = %q, want %q", got, want)
	}
	if got, want := second.Messages[len(second.Messages)-1].ToolCallID, "call_2"; got != want {
		t.Fatalf("second tool call id = %q, want %q", got, want)
	}

	wantEventTypes := []string{
		output.EventTypeContextDiagnostics,
		output.EventTypeContextDiagnostics,
		output.EventTypeTurnStarted,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeTurnFinished,
		output.EventTypeContextDiagnostics,
		output.EventTypeContextDiagnostics,
		output.EventTypeTurnStarted,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeTurnFinished,
		output.EventTypeStopReason,
	}
	if got := eventTypes(events); !equalStrings(got, wantEventTypes) {
		t.Fatalf("event types = %v, want %v", got, wantEventTypes)
	}
}

func TestRunnerKeepsPromptBoundedAndRetainsDurableContext(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "b.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_3", Name: "read", Arguments: map[string]any{"path": "c.txt"}},
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
		execute: func(ctx context.Context, toolName string, input map[string]any) (any, error) {
			return tool.ExecutionResult{
				Value: map[string]any{"contents": "alpha"},
			}, nil
		},
	}

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}},
			ContextState: prompt.DurableContextState{
				ActiveConstraints: []prompt.DurableContextEntry{
					{Text: "do not lose the active constraint", Source: "user", Turn: 1},
				},
				UnresolvedWork: []prompt.DurableContextEntry{
					{Text: "finish the long-running session", Source: "assistant", Turn: 1},
				},
				ActiveFocus: &prompt.DurableContextEntry{
					Text:   "keep prompt assembly policy-driven",
					Source: "assistant",
					Turn:   1,
				},
			},
			Policy: prompt.AssemblyPolicy{
				Compaction:  prompt.CompactionPolicy{SummaryBytes: 256},
				ToolSummary: prompt.ToolSummaryPolicy{MaxBytes: 32},
			},
		},
		Limits: Limits{MaxTurns: 8, MaxTokens: 2000},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 4; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}

	if got, want := len(state.Lineage.Generations), 1; got != want {
		t.Fatalf("lineage generations = %d, want %d", got, want)
	}
	if got, want := len(state.Lineage.FullMessages()), len(state.Conversation); got != want {
		t.Fatalf("lineage/full conversation len = %d, want %d", got, want)
	}
	if got, want := state.Lineage.FullMessages()[0].Content, "start"; got != want {
		t.Fatalf("lineage first message = %q, want %q", got, want)
	}

	lastRequest := providerStub.requests[len(providerStub.requests)-1].Messages
	if !messageContentsContain(lastRequest, "do not lose the active constraint") {
		t.Fatalf("last request did not retain active constraint: %#v", lastRequest)
	}
	if !messageContentsContain(lastRequest, "keep prompt assembly policy-driven") {
		t.Fatalf("last request did not retain active focus: %#v", lastRequest)
	}
	if got, want := state.Context.ActiveFocus.Text, "keep prompt assembly policy-driven"; got != want {
		t.Fatalf("ActiveFocus = %q, want %q", got, want)
	}
}

func TestRunnerEmitsContextDiagnosticsForBudgetPressureAndCompaction(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "b.txt"}},
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
		execute: func(ctx context.Context, toolName string, input map[string]any) (any, error) {
			return tool.ExecutionResult{
				Value: map[string]any{"contents": "alpha"},
			}, nil
		},
	}

	var events []output.Event
	_, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}},
			ContextState: prompt.DurableContextState{
				ActiveConstraints: []prompt.DurableContextEntry{
					{Text: strings.Repeat("constraint ", 8), Source: "user", Turn: 1},
				},
				UnresolvedWork: []prompt.DurableContextEntry{
					{Text: strings.Repeat("unresolved ", 8), Source: "assistant", Turn: 1},
				},
				ActiveFocus: &prompt.DurableContextEntry{
					Text:   strings.Repeat("focus ", 8),
					Source: "assistant",
					Turn:   1,
				},
			},
			Policy: prompt.AssemblyPolicy{
				Budgets: prompt.SourceBudgetModel{
					DurableContextBytes: 64,
				},
				Compaction: prompt.CompactionPolicy{
					SummaryBytes: 64,
				},
			},
		},
		Limits: Limits{MaxTurns: 6, MaxTokens: 100},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var kinds []string
	for _, event := range events {
		if event.Type != output.EventTypeContextDiagnostics {
			continue
		}
		payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
		if !ok {
			t.Fatalf("diagnostic payload type = %T, want output.ContextDiagnosticsEvent", event.Payload)
		}
		kinds = append(kinds, payload.Kind)
	}
	if !containsString(kinds, "budget") {
		t.Fatalf("diagnostic kinds = %v, want budget event", kinds)
	}
	if containsString(kinds, "compaction") {
		t.Fatalf("diagnostic kinds = %v, want no compaction event at this stage", kinds)
	}
}

func TestRunnerEmitsTokenBudgetDiagnosticsForNormalTurns(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
			},
		},
	}
	executor := &fakeExecutor{}

	var events []output.Event
	_, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
		},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "say hello"},
			},
		},
		Limits: Limits{MaxTurns: 1, MaxTokens: 100},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	foundTokenBudget := false
	for _, event := range events {
		if event.Type != output.EventTypeContextDiagnostics {
			continue
		}
		payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
		if !ok {
			t.Fatalf("diagnostic payload type = %T, want output.ContextDiagnosticsEvent", event.Payload)
		}
		if payload.Kind == "budget" && payload.ContextTokens == 4096 {
			foundTokenBudget = true
			if payload.TotalTokens <= 0 {
				t.Fatalf("payload.TotalTokens = %d, want > 0", payload.TotalTokens)
			}
			if payload.Truncated {
				t.Fatalf("payload.Truncated = true, want false for fitting request")
			}
			break
		}
	}
	if !foundTokenBudget {
		t.Fatalf("events = %#v, want token budget diagnostic", events)
	}
}

func TestRunnerRecompactsUntilTheBudgetFits(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: strings.Repeat("first compaction summary ", 155),
				},
				FinishReason: "stop",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "second summary keeps the intent but is much shorter",
				},
				FinishReason: "stop",
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
	executor := &fakeExecutor{}

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         840,
			MaxCompletionTokens: 32,
			SummaryMaxTokens:    32,
		},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: strings.Repeat("initial request ", 72)},
				{Role: provider.MessageRoleAssistant, Content: strings.Repeat("initial answer ", 64)},
				{Role: provider.MessageRoleUser, Content: strings.Repeat("follow up request ", 56)},
				{Role: provider.MessageRoleAssistant, Content: strings.Repeat("follow up answer ", 48)},
			},
			ToolResults: []provider.Message{
				{Role: provider.MessageRoleTool, Content: strings.Repeat("tool output ", 60)},
			},
			Policy: prompt.AssemblyPolicy{
				Compaction: prompt.CompactionPolicy{SummaryBytes: 128},
			},
		},
		Limits: Limits{MaxTurns: 3},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 3; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
	if got, want := len(state.Lineage.Generations), 3; got != want {
		t.Fatalf("lineage generations = %d, want %d", got, want)
	}
	if got, want := state.Lineage.Generations[2].SummaryPrefix[0].Content, "second summary keeps the intent but is much shorter"; got != want {
		t.Fatalf("latest summary prefix = %q, want %q", got, want)
	}

	var compactionCount int
	var sessionHealthCount int
	var budgetCount int
	var compactionCounts []int
	for _, event := range events {
		if event.Type != output.EventTypeContextDiagnostics {
			continue
		}
		payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
		if !ok {
			t.Fatalf("diagnostic payload type = %T, want output.ContextDiagnosticsEvent", event.Payload)
		}
		switch payload.Kind {
		case "compaction":
			if payload.Severity == "compacting" {
				continue
			}
			compactionCount++
			compactionCounts = append(compactionCounts, payload.CompactionCount)
			if payload.Severity == "" {
				t.Fatalf("compaction payload = %#v, want severity", payload)
			}
			if payload.SessionState == "" {
				t.Fatalf("compaction payload = %#v, want session state", payload)
			}
			if payload.RestartGuidance == "" {
				t.Fatalf("compaction payload = %#v, want restart guidance", payload)
			}
		case "session_health":
			if payload.Severity == "compacting" {
				continue
			}
			sessionHealthCount++
			if payload.CompactionCount == 0 {
				t.Fatalf("session health payload = %#v, want compaction count", payload)
			}
			if payload.Severity == "" {
				t.Fatalf("session health payload = %#v, want severity", payload)
			}
		case "budget":
			if payload.PromptTokens > 0 || payload.TotalTokens > 0 {
				budgetCount++
			}
		}
	}
	if got, want := compactionCount, 2; got != want {
		t.Fatalf("compaction events = %d, want %d", got, want)
	}
	if got, want := sessionHealthCount, 2; got != want {
		t.Fatalf("session health events = %d, want %d", got, want)
	}
	if got, want := len(compactionCounts), 2; got != want {
		t.Fatalf("compaction counts = %v, want %d entries", compactionCounts, want)
	}
	if got, want := compactionCounts[0], 1; got != want {
		t.Fatalf("first compaction count = %d, want %d", got, want)
	}
	if got, want := compactionCounts[1], 2; got != want {
		t.Fatalf("second compaction count = %d, want %d", got, want)
	}
	if got, want := budgetCount, 3; got != want {
		t.Fatalf("token budget diagnostics = %d, want %d", got, want)
	}
}
