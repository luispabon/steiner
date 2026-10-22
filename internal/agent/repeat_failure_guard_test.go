package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func TestToolFailureSignatureOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want toolFailureSignature
	}{
		{
			name: "execution error uses kind and message",
			err:  &tool.ToolExecutionError{Kind: "invalid_tool_input", Message: "bad path"},
			want: toolFailureSignature{tool: "bash", kind: "invalid_tool_input", message: "bad path"},
		},
		{
			name: "wrapped execution error is unwrapped",
			err:  fmt.Errorf("run: %w", &tool.ToolExecutionError{Kind: "exit", Message: "failed"}),
			want: toolFailureSignature{tool: "bash", kind: "exit", message: "failed"},
		},
		{
			name: "plain error is tool_error",
			err:  errors.New("boom"),
			want: toolFailureSignature{tool: "bash", kind: "tool_error", message: "boom"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolFailureSignatureOf("bash", tc.err); got != tc.want {
				t.Fatalf("toolFailureSignatureOf() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestObserveToolOutcome(t *testing.T) {
	boom := errors.New("boom")
	other := errors.New("other")
	type step struct {
		name string
		err  error
	}
	tests := []struct {
		name        string
		steps       []step
		wantTripped bool
	}{
		{
			name:        "three identical failures trip",
			steps:       []step{{"bash", boom}, {"bash", boom}, {"bash", boom}},
			wantTripped: true,
		},
		{
			name:  "two identical failures do not trip",
			steps: []step{{"bash", boom}, {"bash", boom}},
		},
		{
			name:  "success resets the streak",
			steps: []step{{"bash", boom}, {"bash", boom}, {"bash", nil}, {"bash", boom}, {"bash", boom}},
		},
		{
			name:        "streak restarts after success and trips again",
			steps:       []step{{"bash", boom}, {"bash", boom}, {"bash", nil}, {"bash", boom}, {"bash", boom}, {"bash", boom}},
			wantTripped: true,
		},
		{
			name:  "different message resets the streak",
			steps: []step{{"bash", boom}, {"bash", boom}, {"bash", other}, {"bash", boom}, {"bash", boom}},
		},
		{
			name:  "different tool resets the streak",
			steps: []step{{"bash", boom}, {"bash", boom}, {"read", boom}, {"read", boom}},
		},
		{
			name: "not dispatched outcomes are ignored",
			steps: []step{
				{"bash", boom},
				{"bash", errNotDispatched},
				{"bash", boom},
				{"bash", errors.Join(errNotDispatched, context.Canceled)},
				{"bash", boom},
			},
			wantTripped: true,
		},
		{
			name:        "trip latches through later success",
			steps:       []step{{"bash", boom}, {"bash", boom}, {"bash", boom}, {"bash", nil}},
			wantTripped: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &turnProgressor{}
			for _, s := range tc.steps {
				p.observeToolOutcome(s.name, s.err)
			}
			if got := p.tripped != nil; got != tc.wantTripped {
				t.Fatalf("tripped = %v, want %v", got, tc.wantTripped)
			}
		})
	}
}

// failureGuardExecutor fails calls whose "mode" argument is "fail" or
// "fail-other" and succeeds otherwise. Executions are counted atomically
// because parallel batches call Execute concurrently.
type failureGuardExecutor struct {
	executed atomic.Int32
}

func (e *failureGuardExecutor) Execute(_ context.Context, _, _ string, input map[string]any) (any, error) {
	e.executed.Add(1)
	mode, _ := input["mode"].(string)
	switch mode {
	case "fail":
		return nil, errors.New("boom")
	case "fail-other":
		return nil, errors.New("other boom")
	default:
		return map[string]any{"status": "ok"}, nil
	}
}

func failureGuardCalls(prefix string, modes ...string) []provider.ToolCall {
	calls := make([]provider.ToolCall, len(modes))
	for i, mode := range modes {
		calls[i] = provider.ToolCall{
			ID:        fmt.Sprintf("%s_%d", prefix, i+1),
			Name:      "bash",
			Arguments: map[string]any{"mode": mode},
		}
	}
	return calls
}

func failureGuardProgressor(ex *failureGuardExecutor, parallel bool) *turnProgressor {
	req := RunRequest{Executor: ex, Events: output.NoopSink{}}
	if parallel {
		req.ParallelClassOf = func(string) ParallelClass { return ParallelClassTool }
		req.MaxParallelTools = 4
	}
	return newTurnProgressor(req, prompt.AssemblyOptions{}, nil)
}

const repeatedBashFailureMessage = `tool "bash" failed 3 times in a row with the same error: boom`

func TestExecuteToolCalls_RepeatedFailureGuard(t *testing.T) {
	tests := []struct {
		name              string
		parallel          bool
		modes             []string
		wantExecuted      int32
		wantStop          bool
		wantNotDispatched int
	}{
		{
			name:              "serial run stops before the fourth call",
			modes:             []string{"fail", "fail", "fail", "ok"},
			wantExecuted:      3,
			wantStop:          true,
			wantNotDispatched: 1,
		},
		{
			name:         "parallel batch applies all results before stopping",
			parallel:     true,
			modes:        []string{"fail", "fail", "fail", "ok"},
			wantExecuted: 4,
			wantStop:     true,
		},
		{
			name:         "parallel batch with success first applies all results before stopping",
			parallel:     true,
			modes:        []string{"ok", "fail", "fail", "fail"},
			wantExecuted: 4,
			wantStop:     true,
		},
		{
			name:         "two identical failures do not stop",
			modes:        []string{"fail", "fail", "ok"},
			wantExecuted: 3,
		},
		{
			name:         "success before the third failure resets the streak",
			modes:        []string{"fail", "fail", "ok", "fail", "fail"},
			wantExecuted: 5,
		},
		{
			name:         "different error message resets the streak",
			modes:        []string{"fail", "fail", "fail-other", "fail", "fail"},
			wantExecuted: 5,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ex := &failureGuardExecutor{}
			p := failureGuardProgressor(ex, tc.parallel)
			calls := failureGuardCalls("call", tc.modes...)
			outcome := p.executeToolCalls(context.Background(), RunState{Lineage: newConversationLineage(nil)}, provider.ChatResponse{Message: provider.Message{ToolCalls: calls}})

			if got := ex.executed.Load(); got != tc.wantExecuted {
				t.Fatalf("executed = %d, want %d", got, tc.wantExecuted)
			}
			if outcome.Stop != tc.wantStop {
				t.Fatalf("Stop = %v, want %v", outcome.Stop, tc.wantStop)
			}
			if tc.wantStop {
				if outcome.State.StopReason != StopReasonError {
					t.Fatalf("StopReason = %q, want %q", outcome.State.StopReason, StopReasonError)
				}
				if outcome.Error == nil || outcome.Error.Error() != repeatedBashFailureMessage {
					t.Fatalf("Error = %v, want %q", outcome.Error, repeatedBashFailureMessage)
				}
			} else if outcome.Error != nil {
				t.Fatalf("Error = %v, want nil", outcome.Error)
			}

			var toolMsgs []Message
			for _, m := range outcome.State.Conversation {
				if m.Role == MessageRoleTool {
					toolMsgs = append(toolMsgs, m)
				}
			}
			if len(toolMsgs) != len(calls) {
				t.Fatalf("tool messages = %d, want %d", len(toolMsgs), len(calls))
			}
			for i, m := range toolMsgs {
				if m.ToolCallID != calls[i].ID {
					t.Fatalf("tool message %d ToolCallID = %q, want %q", i, m.ToolCallID, calls[i].ID)
				}
			}
			for i := len(toolMsgs) - tc.wantNotDispatched; i < len(toolMsgs); i++ {
				if !strings.Contains(toolMsgs[i].Content, "tool not dispatched") {
					t.Fatalf("tool message %d content = %q, want a not dispatched result", i, toolMsgs[i].Content)
				}
			}
		})
	}
}

func TestExecuteToolCalls_RepeatedFailureSpansTurns(t *testing.T) {
	ex := &failureGuardExecutor{}
	p := failureGuardProgressor(ex, false)
	state := RunState{Lineage: newConversationLineage(nil)}
	var outcome turnOutcome
	for turn := 1; turn <= maxIdenticalToolFailures; turn++ {
		calls := failureGuardCalls(fmt.Sprintf("turn%d", turn), "fail")
		outcome = p.executeToolCalls(context.Background(), state, provider.ChatResponse{Message: provider.Message{ToolCalls: calls}})
		state = outcome.State
		wantStop := turn == maxIdenticalToolFailures
		if outcome.Stop != wantStop {
			t.Fatalf("turn %d: Stop = %v, want %v", turn, outcome.Stop, wantStop)
		}
	}
	if outcome.Error == nil || outcome.Error.Error() != repeatedBashFailureMessage {
		t.Fatalf("Error = %v, want %q", outcome.Error, repeatedBashFailureMessage)
	}
	if got := ex.executed.Load(); got != maxIdenticalToolFailures {
		t.Fatalf("executed = %d, want %d", got, maxIdenticalToolFailures)
	}
}

func TestRunnerStopsAfterRepeatedToolFailures(t *testing.T) {
	var modelCalls int
	providerStub := &fakeProvider{
		chatFn: func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
			modelCalls++
			return provider.ChatResponse{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: fmt.Sprintf("call_%d", modelCalls), Name: "bash", Arguments: map[string]any{"mode": "fail"}},
					},
				},
				FinishReason: "tool_calls",
			}, nil
		},
	}
	ex := &failureGuardExecutor{}
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider:      providerStub,
		Executor:      ex,
		Prompt:        prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "fix the bug"}}},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Limits:        Limits{MaxTurns: 10, MaxTokens: 100},
		Events:        output.NoopSink{},
	})
	var reported *ReportedError
	if !errors.As(err, &reported) {
		t.Fatalf("Run() error = %v, want *ReportedError", err)
	}
	if got := reported.Err.Error(); got != repeatedBashFailureMessage {
		t.Fatalf("reported error = %q, want %q", got, repeatedBashFailureMessage)
	}
	if got, want := state.StopReason, StopReasonError; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if modelCalls != maxIdenticalToolFailures {
		t.Fatalf("model calls = %d, want %d", modelCalls, maxIdenticalToolFailures)
	}
	if got := ex.executed.Load(); got != maxIdenticalToolFailures {
		t.Fatalf("executed = %d, want %d", got, maxIdenticalToolFailures)
	}
}
