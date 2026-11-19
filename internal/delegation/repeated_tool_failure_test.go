package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// repeatedFailureProvider always asks for one call to a failing tool, so the
// runner's repeat guard trips after its configured number of responses.
type repeatedFailureProvider struct{ calls int }

func (p *repeatedFailureProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	p.calls++
	return provider.ChatResponse{
		Message: provider.Message{
			Role: provider.MessageRoleAssistant,
			ToolCalls: []provider.ToolCall{{
				ID:        fmt.Sprintf("call_%d", p.calls),
				Name:      "fail",
				Arguments: map[string]any{},
			}},
		},
		FinishReason: "tool_calls",
	}, nil
}

func (*repeatedFailureProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, errors.New("streaming not expected in repeated failure test")
}

func (*repeatedFailureProvider) SupportsUsageStats() bool { return false }

// runRepeatedToolFailure returns the real runner error from a child whose tool
// fails every response, so the repeat guard supplies its typed error.
func runRepeatedToolFailure(t *testing.T) error {
	t.Helper()
	prov := &repeatedFailureProvider{}
	reg := tool.NewRegistry(tool.ToolDef{
		Name:        "fail",
		Description: "always fails",
		Handler: func(context.Context, map[string]any) (any, error) {
			return nil, errors.New("boom")
		},
	})
	exec := tool.NewExecutor(reg, config.Config{}, nil, t.TempDir(), "", tool.Unsandboxed{})
	_, err := agent.NewRunner().Run(context.Background(), agent.RunRequest{
		Provider: prov,
		Executor: exec,
		Tools:    reg.ToProviderSpecs(),
		Prompt: prompt.AssemblyOptions{Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "start"},
		}},
		Limits: agent.Limits{MaxTurns: 10},
		Events: output.NoopSink{},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want the repeated-failure guard error")
	}
	return err
}

func TestRepeatedToolFailureOf(t *testing.T) {
	t.Parallel()

	guardErr := runRepeatedToolFailure(t)
	if !repeatedToolFailureOf(guardErr) {
		t.Fatalf("repeatedToolFailureOf(%v) = false, want true", guardErr)
	}
	if !repeatedToolFailureOf(fmt.Errorf("runner: %w", guardErr)) {
		t.Fatal("repeatedToolFailureOf(wrapped guard error) = false, want true")
	}

	tests := []struct {
		name string
		err  error
	}{
		{name: "nil", err: nil},
		{name: "plain error", err: errors.New("boom")},
		{name: "usage limit joined with repeated failure", err: errors.Join(guardErr, testUsageLimitErr())},
		{name: "cancellation joined with repeated failure", err: errors.Join(guardErr, context.Canceled)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if repeatedToolFailureOf(tt.err) {
				t.Errorf("repeatedToolFailureOf(%v) = true, want false", tt.err)
			}
		})
	}
}

func TestFailedDelegateExecutionRepeatedToolFailureFlag(t *testing.T) {
	t.Parallel()
	spec := Spec{AgentID: "child-repeat", Task: "fail repeatedly"}

	repeated := failedDelegateExecution(spec, agent.RunState{TurnCount: 3}, TokenUsage{},
		fmt.Errorf("runner: %w", runRepeatedToolFailure(t)), nil, newTraceCollector(spec.AgentID, spec.Task), nil)
	repeatedResult, ok := repeated.Value.(Result)
	if !ok {
		t.Fatalf("Value = %T, want Result", repeated.Value)
	}
	if repeatedResult.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", repeatedResult.Status, StatusFailed)
	}
	if !repeatedResult.repeatedToolFailure {
		t.Error("repeatedToolFailure = false, want true for the guard error")
	}

	plain := failedDelegateExecution(spec, agent.RunState{}, TokenUsage{}, errors.New("boom"), nil, newTraceCollector(spec.AgentID, spec.Task), nil)
	plainResult, ok := plain.Value.(Result)
	if !ok {
		t.Fatalf("Value = %T, want Result", plain.Value)
	}
	if plainResult.repeatedToolFailure {
		t.Error("repeatedToolFailure = true, want false for a plain error")
	}

	limit := failedDelegateExecution(spec, agent.RunState{}, TokenUsage{}, testUsageLimitErr(), nil, newTraceCollector(spec.AgentID, spec.Task), nil)
	limitResult, ok := limit.Value.(Result)
	if !ok {
		t.Fatalf("Value = %T, want Result", limit.Value)
	}
	if limitResult.repeatedToolFailure {
		t.Error("repeatedToolFailure = true, want false for a usage limit")
	}
}

func TestProjectToolResultRepeatedToolFailureFollowUp(t *testing.T) {
	t.Parallel()

	t.Run("persisted failure carries the follow-up guidance", func(t *testing.T) {
		t.Parallel()
		result := Result{
			AgentID:             "child-repeat",
			Status:              StatusFailed,
			Reason:              "delegation failed: repeated tool failure",
			repeatedToolFailure: true,
			persisted:           true,
		}
		envelope := result.ProjectToolResult()
		want := "delegation failed: repeated tool failure; " + repeatedToolFailureFollowUpHint
		if envelope.Reason != want {
			t.Errorf("Reason = %q, want %q", envelope.Reason, want)
		}
		if envelope.Continuation == nil || envelope.Continuation.AgentID != "child-repeat" {
			t.Errorf("Continuation = %+v, want the persisted agent id", envelope.Continuation)
		}
		for _, wantText := range []string{"follow_up", "same agent_id", "prior conversation is retained", "follow-up limit", "workspace availability", "execution mode"} {
			if !strings.Contains(envelope.Reason, wantText) {
				t.Errorf("Reason = %q, missing %q", envelope.Reason, wantText)
			}
		}
	})

	t.Run("empty reason still gains the guidance", func(t *testing.T) {
		t.Parallel()
		result := Result{AgentID: "child-repeat", Status: StatusFailed, repeatedToolFailure: true, persisted: true}
		want := "unknown failure; " + repeatedToolFailureFollowUpHint
		if got := result.ProjectToolResult().Reason; got != want {
			t.Errorf("Reason = %q, want %q", got, want)
		}
	})

	tests := []struct {
		name   string
		result Result
	}{
		{
			name: "unsaved session",
			result: Result{
				AgentID:             "child-repeat",
				Status:              StatusFailed,
				Reason:              "delegation failed: repeated tool failure",
				repeatedToolFailure: true,
			},
		},
		{
			name: "missing agent id",
			result: Result{
				Status:              StatusFailed,
				Reason:              "delegation failed: repeated tool failure",
				repeatedToolFailure: true,
				persisted:           true,
			},
		},
		{
			name: "other failure",
			result: Result{
				AgentID:   "child-repeat",
				Status:    StatusFailed,
				Reason:    "delegation failed: repeated tool failure",
				persisted: true,
			},
		},
		{
			name: "cancelled result",
			result: Result{
				AgentID:             "child-repeat",
				Status:              StatusCancelled,
				Reason:              "cancelled by request",
				repeatedToolFailure: true,
				persisted:           true,
			},
		},
		{
			name: "partial result",
			result: Result{
				AgentID:             "child-repeat",
				Status:              StatusPartial,
				repeatedToolFailure: true,
				persisted:           true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if reason := tt.result.ProjectToolResult().Reason; strings.Contains(reason, "follow_up") {
				t.Errorf("Reason = %q, want no follow-up guidance", reason)
			}
		})
	}
}
