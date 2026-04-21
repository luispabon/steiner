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
		output.EventTypeModelCallStarted,
		output.EventTypeModelCallFinished,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeModelCallStarted,
		output.EventTypeModelCallFinished,
		output.EventTypeStopReason,
	}
	if got := eventTypes(events); !equalStrings(got, wantEventTypes) {
		t.Fatalf("event types = %v, want %v", got, wantEventTypes)
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
	approver := NewEventingApprover(output.NoopSink{}, tool.ApproverFunc(func(ctx context.Context, req tool.ApprovalRequest) (tool.ApprovalResponse, error) {
		gotPreview = req.Preview
		return tool.ApprovalResponse{Allow: true, Message: "ok"}, nil
	}))

	resp, err := approver.Approve(context.Background(), tool.ApprovalRequest{
		Tool:    tool.ToolDef{Name: "bash"},
		Mode:    config.ApprovalModePrompt,
		Preview: preview,
	})
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if !resp.Allow {
		t.Fatal("Approve() = false, want true")
	}
	if got, want := gotPreview.Summary(), preview.Summary(); got != want {
		t.Fatalf("forwarded preview summary = %q, want %q", got, want)
	}
}

func TestEventingApproverEmitsLifecycleEvents(t *testing.T) {
	var events []output.Event
	approver := NewEventingApprover(output.SinkFunc(func(event output.Event) { events = append(events, event) }), tool.ApproverFunc(func(ctx context.Context, req tool.ApprovalRequest) (tool.ApprovalResponse, error) {
		return tool.ApprovalResponse{Allow: true, Message: "ok"}, nil
	}))

	resp, err := approver.Approve(context.Background(), tool.ApprovalRequest{
		Tool: tool.ToolDef{Name: "write"},
		Mode: config.ApprovalModePrompt,
	})
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if !resp.Allow {
		t.Fatal("Approve() = false, want true")
	}
	if got, want := eventTypes(events), []string{output.EventTypeApprovalRequested, output.EventTypeApprovalAccepted}; !equalStrings(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
}

type fakeProvider struct {
	requests  []provider.ChatRequest
	responses []provider.ChatResponse
}

func (p *fakeProvider) ChatCompletion(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if len(p.responses) == 0 {
		return provider.ChatResponse{}, errors.New("no response configured")
	}
	resp := p.responses[0]
	p.responses = p.responses[1:]
	return resp, nil
}

func (p *fakeProvider) StreamChatCompletion(ctx context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
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
		output.EventTypeModelCallStarted,
		output.EventTypeModelCallFinished,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeModelCallStarted,
		output.EventTypeModelCallFinished,
		output.EventTypeStopReason,
	}
	if got := eventTypes(events); !equalStrings(got, wantEventTypes) {
		t.Fatalf("event types = %v, want %v", got, wantEventTypes)
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
}
