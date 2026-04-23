package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
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
		Provider:    providerStub,
		Executor:    executor,
		Tools:       []provider.ToolSpec{{Type: "function", Function: provider.ToolFunctionSpec{Name: "read", Description: "Read files", Parameters: map[string]any{"type": "object"}}}},
		Prompt:      prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "fix the bug"}}, ProjectContextBudgetBytes: 128},
		Model:       "test-model",
		Temperature: float64Ptr(0.1),
		MaxTokens:   intPtr(64),
		Limits:      Limits{MaxTurns: 4, MaxTokens: 50},
		Events:      output.SinkFunc(func(event output.Event) { events = append(events, event) }),
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
		output.EventTypeTurnStarted,
		output.EventTypeModelCallStarted,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeModelCallFinished,
		output.EventTypeAssistantMessage,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeTurnFinished,
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
		Limits: Limits{MaxTurns: 3, MaxTokens: 10},
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

func TestEventingApproverForwardsPreviewToInnerApprover(t *testing.T) {
	preview := tool.ApprovalPreview{
		Tool:    "bash",
		WorkDir: "/repo",
		Timeout: 3 * time.Second,
		Fields: []tool.PreviewField{
			{Name: "cwd", Value: "/repo/subdir"},
			{Name: "command", Value: "pwd"},
		},
	}

	var gotPreview tool.ApprovalPreview
	approver := NewEventingApprover(output.NoopSink{}, tool.ApprovalResponderFunc(func(ctx context.Context, req tool.ApprovalRequest) error {
		gotPreview = req.Preview
		req.Response <- tool.ApprovalResponse{Allow: true, Message: "ok"}
		return nil
	}))

	response := make(chan tool.ApprovalResponse, 1)
	err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "bash"},
		Mode:     config.ApprovalModePrompt,
		Preview:  preview,
		Response: response,
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	resp := <-response
	if !resp.Allow {
		t.Fatal("approval response = false, want true")
	}
	if got, want := gotPreview.Summary(), preview.Summary(); got != want {
		t.Fatalf("forwarded preview summary = %q, want %q", got, want)
	}
}

func TestEventingApproverEmitsLifecycleEvents(t *testing.T) {
	var events []output.Event
	approver := NewEventingApprover(output.SinkFunc(func(event output.Event) { events = append(events, event) }), tool.ApprovalResponderFunc(func(ctx context.Context, req tool.ApprovalRequest) error {
		req.Response <- tool.ApprovalResponse{Allow: true, Message: "ok"}
		return nil
	}))

	response := make(chan tool.ApprovalResponse, 1)
	err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "write"},
		Mode:     config.ApprovalModePrompt,
		Response: response,
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	resp := <-response
	if !resp.Allow {
		t.Fatal("approval response = false, want true")
	}
	if got, want := eventTypes(events), []string{output.EventTypeApprovalRequested, output.EventTypeApprovalAccepted}; !equalStrings(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
}

type fakeProvider struct {
	requests  []provider.ChatRequest
	responses []provider.ChatResponse
	chatFn    func(context.Context, provider.ChatRequest) (provider.ChatResponse, error)
	streamFn  func(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error)
}

func (p *fakeProvider) ChatCompletion(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if p.chatFn != nil {
		return p.chatFn(ctx, req)
	}
	if len(p.responses) == 0 {
		return provider.ChatResponse{}, errors.New("no response configured")
	}
	resp := p.responses[0]
	p.responses = p.responses[1:]
	return resp, nil
}

func (p *fakeProvider) StreamChatCompletion(ctx context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	if p.streamFn != nil {
		p.requests = append(p.requests, req)
		return p.streamFn(ctx, req)
	}
	return nil, errors.New("stream not used")
}

func (p *fakeProvider) SupportsUsageStats() bool { return true }

type fakeExecutor struct {
	calls []struct {
		tool string
		args map[string]any
	}
	execute func(context.Context, string, map[string]any) (any, error)
}

func (e *fakeExecutor) Execute(ctx context.Context, toolName string, input map[string]any) (any, error) {
	e.calls = append(e.calls, struct {
		tool string
		args map[string]any
	}{tool: toolName, args: cloneInput(input)})
	return e.execute(ctx, toolName, input)
}

type cancelContextKey struct{}

func rolesOf(messages []provider.Message) []string {
	roles := make([]string, 0, len(messages))
	for _, message := range messages {
		roles = append(roles, string(message.Role))
	}
	return roles
}

func eventTypes(events []output.Event) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func messageContentsContain(messages []provider.Message, needle string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, needle) {
			return true
		}
	}
	return false
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSequence(values, sequence []string) bool {
	if len(sequence) == 0 || len(values) < len(sequence) {
		return false
	}
	for start := 0; start <= len(values)-len(sequence); start++ {
		match := true
		for i := range sequence {
			if values[start+i] != sequence[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func float64Ptr(v float64) *float64 { return &v }

func intPtr(v int) *int { return &v }

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
		Limits: Limits{MaxTurns: 2, MaxTokens: 10},
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
				Retention:   prompt.RetentionPolicy{RecentTurns: 1},
				Compaction:  prompt.CompactionPolicy{SummaryBytes: 256},
				ToolSummary: prompt.ToolSummaryPolicy{MaxBytes: 32},
			},
		},
		Limits: Limits{MaxTurns: 8, MaxTokens: 100},
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
				Retention: prompt.RetentionPolicy{RecentTurns: 1},
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

func TestRunnerEmitsDiagnosticsForTruncatedRetainedConversation(t *testing.T) {
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
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "older request"},
				{Role: provider.MessageRoleAssistant, Content: "older reply"},
				{Role: provider.MessageRoleUser, Content: "retained request payload"},
				{Role: provider.MessageRoleAssistant, Content: "retained reply payload"},
			},
			Policy: prompt.AssemblyPolicy{
				Budgets: prompt.SourceBudgetModel{
					ConversationBytes: 12,
				},
				Retention: prompt.RetentionPolicy{RecentTurns: 1},
				Compaction: prompt.CompactionPolicy{
					SummaryBytes: 64,
				},
			},
		},
		Limits: Limits{MaxTurns: 2, MaxTokens: 100},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	foundConversationBudget := false
	for _, event := range events {
		if event.Type != output.EventTypeContextDiagnostics {
			continue
		}
		payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
		if !ok {
			t.Fatalf("diagnostic payload type = %T, want output.ContextDiagnosticsEvent", event.Payload)
		}
		if payload.Kind == "budget" && payload.Scope == string(prompt.ContextSourceConversation) && payload.Truncated {
			foundConversationBudget = true
			break
		}
	}
	if !foundConversationBudget {
		t.Fatalf("events = %#v, want truncated conversation budget diagnostic", events)
	}
}

func TestConversationGenerationViewsArePrefixAware(t *testing.T) {
	generation := newConversationGeneration(7,
		[]Message{
			{Role: MessageRoleSummary, Content: "summary prefix one"},
			{Role: MessageRoleSummary, Content: "summary prefix two"},
		},
		[]Message{
			{Role: MessageRoleUser, Content: "raw user"},
			{Role: MessageRoleAssistant, Content: "raw assistant"},
		},
	)

	full := generation.FullMessages()
	if got, want := len(full), 4; got != want {
		t.Fatalf("full len = %d, want %d", got, want)
	}
	if got, want := full[0].Content, "summary prefix one"; got != want {
		t.Fatalf("full[0] = %q, want %q", got, want)
	}
	if got, want := full[3].Content, "raw assistant"; got != want {
		t.Fatalf("full[3] = %q, want %q", got, want)
	}

	stripped := generation.SummaryPrefixStrippedMessages()
	if got, want := len(stripped), 2; got != want {
		t.Fatalf("stripped len = %d, want %d", got, want)
	}
	if got, want := stripped[0].Content, "raw user"; got != want {
		t.Fatalf("stripped[0] = %q, want %q", got, want)
	}
	if got, want := stripped[1].Content, "raw assistant"; got != want {
		t.Fatalf("stripped[1] = %q, want %q", got, want)
	}

	full[0].Content = "changed"
	stripped[0].Content = "changed"
	if got, want := generation.SummaryPrefix[0].Content, "summary prefix one"; got != want {
		t.Fatalf("generation summary prefix mutated = %q, want %q", got, want)
	}
	if got, want := generation.Messages[0].Content, "raw user"; got != want {
		t.Fatalf("generation raw messages mutated = %q, want %q", got, want)
	}
}

func TestConversationLineageChoosesHighestFidelityCandidateDeterministically(t *testing.T) {
	lineage := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{
				{Role: MessageRoleUser, Content: "gen1 user"},
				{Role: MessageRoleAssistant, Content: "gen1 assistant"},
			}),
			newConversationGeneration(2,
				[]Message{{Role: MessageRoleSummary, Content: "summary for gen2"}},
				[]Message{
					{Role: MessageRoleUser, Content: "gen2 user"},
					{Role: MessageRoleAssistant, Content: "gen2 assistant"},
				},
			),
			newConversationGeneration(3,
				[]Message{
					{Role: MessageRoleSummary, Content: "summary for gen2"},
					{Role: MessageRoleSummary, Content: "summary for gen3"},
				},
				[]Message{
					{Role: MessageRoleUser, Content: "gen3 user"},
					{Role: MessageRoleAssistant, Content: "gen3 assistant"},
				},
			),
		},
		NextGenerationID: 4,
	}

	candidates := lineage.Candidates()
	if got, want := len(candidates), 5; got != want {
		t.Fatalf("candidate count = %d, want %d", got, want)
	}
	if got, want := candidates[0].GenerationID, 3; got != want {
		t.Fatalf("candidate[0] generation = %d, want %d", got, want)
	}
	if got, want := candidates[0].View, ConversationViewFull; got != want {
		t.Fatalf("candidate[0] view = %q, want %q", got, want)
	}
	if got, want := candidates[1].View, ConversationViewSummaryPrefixStripped; got != want {
		t.Fatalf("candidate[1] view = %q, want %q", got, want)
	}
	if got, want := candidates[1].Messages[0].Content, "gen3 user"; got != want {
		t.Fatalf("candidate[1] first message = %q, want %q", got, want)
	}
	if got, want := candidates[2].GenerationID, 2; got != want {
		t.Fatalf("candidate[2] generation = %d, want %d", got, want)
	}

	candidate, ok := lineage.HighestFidelityCandidate(func(messages []Message) bool {
		return len(messages) <= 2
	})
	if !ok {
		t.Fatal("HighestFidelityCandidate() ok = false, want true")
	}
	if got, want := candidate.GenerationID, 3; got != want {
		t.Fatalf("candidate generation = %d, want %d", got, want)
	}
	if got, want := candidate.View, ConversationViewSummaryPrefixStripped; got != want {
		t.Fatalf("candidate view = %q, want %q", got, want)
	}
	if got, want := len(candidate.Messages), 2; got != want {
		t.Fatalf("candidate messages len = %d, want %d", got, want)
	}
	if got, want := candidate.Messages[0].Content, "gen3 user"; got != want {
		t.Fatalf("candidate first message = %q, want %q", got, want)
	}

	fallback, ok := lineage.HighestFidelityCandidate(func(messages []Message) bool {
		return len(messages) == 0
	})
	if !ok {
		t.Fatal("fallback candidate ok = false, want true")
	}
	if got, want := fallback.GenerationID, 3; got != want {
		t.Fatalf("fallback generation = %d, want %d", got, want)
	}
	if got, want := fallback.View, ConversationViewFull; got != want {
		t.Fatalf("fallback view = %q, want %q", got, want)
	}
}

func TestConversationLineagePrunesObsoleteGenerationsAfterTakeover(t *testing.T) {
	lineage := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{{Role: MessageRoleUser, Content: "old user"}}),
			newConversationGeneration(2, []Message{{Role: MessageRoleSummary, Content: "summary"}}, []Message{{Role: MessageRoleUser, Content: "new user"}}),
		},
		NextGenerationID: 3,
	}

	pruned := lineage.PruneObsolete()
	if got, want := len(pruned.Generations), 1; got != want {
		t.Fatalf("pruned generation count = %d, want %d", got, want)
	}
	if got, want := pruned.Generations[0].ID, 2; got != want {
		t.Fatalf("pruned generation id = %d, want %d", got, want)
	}
	if got, want := len(pruned.FullMessages()), 2; got != want {
		t.Fatalf("pruned full message count = %d, want %d", got, want)
	}
	if got, want := pruned.FullMessages()[0].Content, "summary"; got != want {
		t.Fatalf("pruned full[0] = %q, want %q", got, want)
	}
	if got, want := pruned.FullMessages()[1].Content, "new user"; got != want {
		t.Fatalf("pruned full[1] = %q, want %q", got, want)
	}
}

func TestRunStateUpdateHelpersPreserveDurableContext(t *testing.T) {
	original := RunState{
		TurnCount:  3,
		TokenCount: 27,
		StopReason: StopReasonMaxTokens,
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "keep working"},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: "keep working"},
		}),
		Context: ContextState{
			ActiveConstraints: []ActiveConstraint{
				{Text: "do not change public APIs", Source: "user", Turn: 1},
			},
			UnresolvedWork: []UnresolvedWorkItem{
				{Text: "tighten retry handling", Source: "assistant", Turn: 2},
			},
			ActiveFocus: &ActiveFocus{
				Text:   "finish the agent loop",
				Source: "assistant",
				Turn:   3,
			},
			RetainedSummaries: []RetainedSummary{
				{Title: "earlier progress", Text: "implemented the scheduler", Source: "compaction", Turn: 2},
			},
		},
	}

	withConversation := original.WithConversation([]Message{
		{Role: MessageRoleAssistant, Content: "new turn"},
	})

	if got, want := withConversation.TurnCount, original.TurnCount; got != want {
		t.Fatalf("TurnCount = %d, want %d", got, want)
	}
	if got, want := withConversation.TokenCount, original.TokenCount; got != want {
		t.Fatalf("TokenCount = %d, want %d", got, want)
	}
	if got, want := withConversation.StopReason, original.StopReason; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(withConversation.Conversation), 1; got != want {
		t.Fatalf("Conversation len = %d, want %d", got, want)
	}
	if got, want := len(withConversation.Lineage.Generations), 1; got != want {
		t.Fatalf("Lineage generations = %d, want %d", got, want)
	}
	if got, want := withConversation.Lineage.FullMessages()[0].Content, "new turn"; got != want {
		t.Fatalf("Lineage full content = %q, want %q", got, want)
	}
	if got, want := withConversation.Context.ActiveConstraints[0].Text, "do not change public APIs"; got != want {
		t.Fatalf("ActiveConstraint text = %q, want %q", got, want)
	}
	if got, want := withConversation.Context.UnresolvedWork[0].Text, "tighten retry handling"; got != want {
		t.Fatalf("UnresolvedWork text = %q, want %q", got, want)
	}
	if got, want := withConversation.Context.ActiveFocus.Text, "finish the agent loop"; got != want {
		t.Fatalf("ActiveFocus text = %q, want %q", got, want)
	}
	if got, want := withConversation.Context.RetainedSummaries[0].Text, "implemented the scheduler"; got != want {
		t.Fatalf("RetainedSummary text = %q, want %q", got, want)
	}

	withConversation.Context.ActiveConstraints[0].Text = "changed"
	withConversation.Context.UnresolvedWork[0].Text = "changed"
	withConversation.Context.ActiveFocus.Text = "changed"
	withConversation.Context.RetainedSummaries[0].Text = "changed"

	if got, want := original.Context.ActiveConstraints[0].Text, "do not change public APIs"; got != want {
		t.Fatalf("original constraint text = %q, want %q", got, want)
	}
	if got, want := original.Context.UnresolvedWork[0].Text, "tighten retry handling"; got != want {
		t.Fatalf("original unresolved work text = %q, want %q", got, want)
	}
	if got, want := original.Context.ActiveFocus.Text, "finish the agent loop"; got != want {
		t.Fatalf("original active focus text = %q, want %q", got, want)
	}
	if got, want := original.Context.RetainedSummaries[0].Text, "implemented the scheduler"; got != want {
		t.Fatalf("original retained summary text = %q, want %q", got, want)
	}
	if got, want := original.Lineage.FullMessages()[0].Content, "keep working"; got != want {
		t.Fatalf("original lineage content = %q, want %q", got, want)
	}

	withContext := original.WithContext(ContextState{
		ActiveFocus: &ActiveFocus{
			Text:   "render compacted context blocks",
			Source: "planner",
			Turn:   4,
		},
	})

	if got, want := len(withContext.Conversation), len(original.Conversation); got != want {
		t.Fatalf("Conversation len = %d, want %d", got, want)
	}
	if got, want := withContext.Conversation[0].Content, "keep working"; got != want {
		t.Fatalf("Conversation content = %q, want %q", got, want)
	}
	if got, want := withContext.Context.ActiveFocus.Text, "render compacted context blocks"; got != want {
		t.Fatalf("replacement ActiveFocus text = %q, want %q", got, want)
	}
	if got, want := withContext.Lineage.FullMessages()[0].Content, "keep working"; got != want {
		t.Fatalf("WithContext lineage content = %q, want %q", got, want)
	}
}
