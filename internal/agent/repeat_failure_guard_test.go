package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

type guardOutcome struct {
	name string
	err  error
}

func repeatGuardResponses(responses ...[]guardOutcome) [][]guardOutcome {
	return responses
}

// TestToolFailureGuardCounts checks the consecutive-response bookkeeping
// directly: one count per distinct signature per response, advanced only when
// the signature recurs in the next committed response.
func TestToolFailureGuardCounts(t *testing.T) {
	boom := errors.New("boom")
	other := errors.New("other boom")

	tests := []struct {
		name        string
		responses   [][]guardOutcome
		wantTripped bool
		wantTool    string
		wantMessage string
	}{
		{
			name:      "three identical failures in one response count once",
			responses: repeatGuardResponses([]guardOutcome{{"bash", boom}, {"bash", boom}, {"bash", boom}}),
		},
		{
			name: "three consecutive responses trip",
			responses: repeatGuardResponses(
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", boom}},
			),
			wantTripped: true,
			wantTool:    "bash",
			wantMessage: "boom",
		},
		{
			name: "successful sibling does not reset",
			responses: repeatGuardResponses(
				[]guardOutcome{{"bash", nil}, {"bash", boom}},
				[]guardOutcome{{"bash", nil}, {"bash", boom}},
				[]guardOutcome{{"bash", boom}},
			),
			wantTripped: true,
			wantTool:    "bash",
			wantMessage: "boom",
		},
		{
			name: "changing-error sibling does not reset",
			responses: repeatGuardResponses(
				[]guardOutcome{{"bash", boom}, {"bash", other}},
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", other}, {"bash", boom}},
			),
			wantTripped: true,
			wantTool:    "bash",
			wantMessage: "boom",
		},
		{
			name: "absent signature drops out",
			responses: repeatGuardResponses(
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", other}},
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", boom}},
			),
		},
		{
			name: "all-success response clears counts",
			responses: repeatGuardResponses(
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", nil}},
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", boom}},
			),
		},
		{
			name: "recovery then three recurrences trip",
			responses: repeatGuardResponses(
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", nil}},
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", boom}},
			),
			wantTripped: true,
			wantTool:    "bash",
			wantMessage: "boom",
		},
		{
			name: "different tool is a separate signature",
			responses: repeatGuardResponses(
				[]guardOutcome{{"read", boom}},
				[]guardOutcome{{"read", boom}},
				[]guardOutcome{{"bash", boom}},
			),
		},
		{
			name: "not dispatched response is neutral",
			responses: repeatGuardResponses(
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", boom}},
				[]guardOutcome{{"bash", errNotDispatched}},
				[]guardOutcome{{"bash", boom}},
			),
			wantTripped: true,
			wantTool:    "bash",
			wantMessage: "boom",
		},
		{
			name: "multiple tripping signatures choose deterministically",
			responses: repeatGuardResponses(
				[]guardOutcome{{"bash", other}, {"bash", boom}},
				[]guardOutcome{{"bash", other}, {"bash", boom}},
				[]guardOutcome{{"bash", other}, {"bash", boom}},
			),
			wantTripped: true,
			wantTool:    "bash",
			wantMessage: "boom",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &turnProgressor{}
			for _, response := range tc.responses {
				p.beginToolResponse()
				for _, o := range response {
					p.observeToolOutcome(o.name, o.err)
				}
				p.commitToolResponse()
			}
			if got := p.tripped != nil; got != tc.wantTripped {
				t.Fatalf("tripped = %v, want %v", got, tc.wantTripped)
			}
			if !tc.wantTripped {
				return
			}
			if p.tripped.tool != tc.wantTool || p.tripped.message != tc.wantMessage {
				t.Fatalf("tripped = %+v, want tool %q message %q", *p.tripped, tc.wantTool, tc.wantMessage)
			}
			outcome, ok := p.stopOnRepeatedFailure(RunState{})
			if !ok || !outcome.Stop {
				t.Fatalf("stopOnRepeatedFailure() = %+v, %v, want a stop", outcome, ok)
			}
			var repeated *RepeatedToolFailureError
			if !errors.As(outcome.Error, &repeated) {
				t.Fatalf("errors.As(%v, *RepeatedToolFailureError) = false, want true", outcome.Error)
			}
			if want := repeatedFailureMessage(tc.wantTool, tc.wantMessage); repeated.Error() != want {
				t.Fatalf("Error = %q, want %q", repeated.Error(), want)
			}
		})
	}
}

// TestRepeatedToolFailureErrorWrappedDetection checks that delegation can find
// the guard error through the runner's ReportedError wrapper.
func TestRepeatedToolFailureErrorWrappedDetection(t *testing.T) {
	guardErr := &RepeatedToolFailureError{sig: toolFailureSignature{tool: "bash", kind: "tool_error", message: "boom"}}
	wrapped := fmt.Errorf("sub-agent failed: %w", &ReportedError{Err: guardErr})

	var detected *RepeatedToolFailureError
	if !errors.As(wrapped, &detected) {
		t.Fatalf("errors.As(%v, *RepeatedToolFailureError) = false, want true", wrapped)
	}
	if detected != guardErr {
		t.Fatalf("errors.As detected %p, want %p", detected, guardErr)
	}
	if want := repeatedFailureMessage("bash", "boom"); detected.Error() != want {
		t.Fatalf("Error() = %q, want %q", detected.Error(), want)
	}
}

// failureGuardExecutor fails calls whose "mode" argument is "fail" or
// "fail-other", hands off for "handoff", and succeeds otherwise. Executions
// are counted atomically because parallel batches call Execute concurrently.
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
	case "handoff":
		return tool.WorkflowHandoffAccepted{}, nil
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

func repeatedFailureMessage(toolName, message string) string {
	return fmt.Sprintf("tool %q failed with the same error in %d consecutive model responses: %s", toolName, maxRepeatedFailureResponses, message)
}

func TestExecuteToolCalls_RepeatFailureGuardAcrossResponses(t *testing.T) {
	tests := []struct {
		name          string
		parallel      bool
		responses     [][]string
		wantStopAfter int // 1-based response index; 0 when the guard never trips
		wantMessage   string
	}{
		{
			name:      "three identical failures in one serial response do not stop",
			responses: [][]string{{"fail", "fail", "fail"}},
		},
		{
			name:      "three identical failures in one parallel response do not stop",
			parallel:  true,
			responses: [][]string{{"fail", "fail", "fail"}},
		},
		{
			name:          "same signature across three serial responses stops",
			responses:     [][]string{{"fail"}, {"fail"}, {"fail"}},
			wantStopAfter: 3,
			wantMessage:   "boom",
		},
		{
			name:          "successful sibling does not reset parallel responses",
			parallel:      true,
			responses:     [][]string{{"ok", "fail"}, {"ok", "fail"}, {"fail"}},
			wantStopAfter: 3,
			wantMessage:   "boom",
		},
		{
			name:          "changing error siblings still stop",
			responses:     [][]string{{"fail", "fail-other"}, {"fail"}, {"fail-other", "fail"}},
			wantStopAfter: 3,
			wantMessage:   "boom",
		},
		{
			name:      "interleaved signature resets the consecutive count",
			responses: [][]string{{"fail"}, {"fail"}, {"fail-other"}, {"fail"}, {"fail"}},
		},
		{
			name:          "recovery then three recurrences stops",
			responses:     [][]string{{"fail"}, {"fail"}, {"ok"}, {"fail"}, {"fail"}, {"fail"}},
			wantStopAfter: 6,
			wantMessage:   "boom",
		},
		{
			name:          "multiple tripping signatures choose deterministically",
			responses:     [][]string{{"fail-other", "fail"}, {"fail-other", "fail"}, {"fail-other", "fail"}},
			wantStopAfter: 3,
			wantMessage:   "boom",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ex := &failureGuardExecutor{}
			p := failureGuardProgressor(ex, tc.parallel)
			state := RunState{Lineage: newConversationLineage(nil)}
			totalCalls := 0
			for i, modes := range tc.responses {
				totalCalls += len(modes)
				calls := failureGuardCalls(fmt.Sprintf("turn%d", i+1), modes...)
				outcome := p.executeToolCalls(context.Background(), state, provider.ChatResponse{Message: provider.Message{ToolCalls: calls}})
				state = outcome.State
				wantStop := tc.wantStopAfter == i+1
				if outcome.Stop != wantStop {
					t.Fatalf("response %d: Stop = %v, want %v", i+1, outcome.Stop, wantStop)
				}
				if !wantStop {
					if outcome.Error != nil {
						t.Fatalf("response %d: Error = %v, want nil", i+1, outcome.Error)
					}
					continue
				}
				if state.StopReason != StopReasonError {
					t.Fatalf("StopReason = %q, want %q", state.StopReason, StopReasonError)
				}
				if want := repeatedFailureMessage("bash", tc.wantMessage); outcome.Error == nil || outcome.Error.Error() != want {
					t.Fatalf("Error = %v, want %q", outcome.Error, want)
				}
			}
			if got := ex.executed.Load(); got != int32(totalCalls) {
				t.Fatalf("executed = %d, want %d", got, totalCalls)
			}
			var toolMessages int
			for _, m := range state.Conversation {
				if m.Role != MessageRoleTool {
					continue
				}
				toolMessages++
				if strings.Contains(m.Content, "tool not dispatched") {
					t.Fatalf("tool message %q recorded a not dispatched result", m.Content)
				}
			}
			if toolMessages != totalCalls {
				t.Fatalf("tool messages = %d, want %d", toolMessages, totalCalls)
			}
		})
	}
}

// TestExecuteToolCalls_InterruptedResponseDoesNotCommit verifies that a
// response ended by cancellation, a limit timeout, or workflow handoff leaves
// the guard counts untouched, including its all-success clearing effect.
func TestExecuteToolCalls_InterruptedResponseDoesNotCommit(t *testing.T) {
	tests := []struct {
		name            string
		interruptedMode string
		ctx             func() (context.Context, context.CancelFunc)
		wantStopReason  StopReason
	}{
		{
			name:            "cancellation",
			interruptedMode: "ok",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			wantStopReason: StopReasonCancelled,
		},
		{
			name:            "limit timeout",
			interruptedMode: "ok",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithTimeoutCause(context.Background(), time.Millisecond, errTurnTimeout)
				<-ctx.Done()
				return ctx, cancel
			},
			wantStopReason: StopReasonError,
		},
		{
			name:            "workflow handoff",
			interruptedMode: "handoff",
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			wantStopReason: StopReasonWorkflowHandoff,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ex := &failureGuardExecutor{}
			p := failureGuardProgressor(ex, false)
			state := RunState{Lineage: newConversationLineage(nil)}
			for i := 0; i < 2; i++ {
				calls := failureGuardCalls(fmt.Sprintf("turn%d", i+1), "fail")
				outcome := p.executeToolCalls(context.Background(), state, provider.ChatResponse{Message: provider.Message{ToolCalls: calls}})
				if outcome.Stop {
					t.Fatalf("response %d stopped early: %v", i+1, outcome.Error)
				}
				state = outcome.State
			}

			ctx, cancel := tc.ctx()
			defer cancel()
			calls := failureGuardCalls("interrupted", tc.interruptedMode)
			outcome := p.executeToolCalls(ctx, state, provider.ChatResponse{Message: provider.Message{ToolCalls: calls}})
			if !outcome.Stop {
				t.Fatal("interrupted response did not stop")
			}
			if outcome.State.StopReason != tc.wantStopReason {
				t.Fatalf("StopReason = %q, want %q", outcome.State.StopReason, tc.wantStopReason)
			}

			calls = failureGuardCalls("after", "fail")
			outcome = p.executeToolCalls(context.Background(), outcome.State, provider.ChatResponse{Message: provider.Message{ToolCalls: calls}})
			if !outcome.Stop || outcome.State.StopReason != StopReasonError {
				t.Fatalf("after interrupted response: Stop = %v, StopReason = %q, Error = %v, want the guard to trip", outcome.Stop, outcome.State.StopReason, outcome.Error)
			}
		})
	}
}

func TestRunnerStopsAfterRepeatedToolFailures(t *testing.T) {
	var modelCalls int
	providerStub := &fakeProvider{
		chatFn: func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
			modelCalls++
			return provider.ChatResponse{
				Message: provider.Message{
					Role:      provider.MessageRoleAssistant,
					ToolCalls: failureGuardCalls(fmt.Sprintf("turn%d", modelCalls), "fail", "fail", "fail"),
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
	if want := repeatedFailureMessage("bash", "boom"); reported.Err.Error() != want {
		t.Fatalf("reported error = %q, want %q", reported.Err.Error(), want)
	}
	var repeated *RepeatedToolFailureError
	if !errors.As(err, &repeated) {
		t.Fatalf("errors.As(%v, *RepeatedToolFailureError) = false, want true", err)
	}
	if got, want := state.StopReason, StopReasonError; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if modelCalls != maxRepeatedFailureResponses {
		t.Fatalf("model calls = %d, want %d", modelCalls, maxRepeatedFailureResponses)
	}
	if got, want := ex.executed.Load(), int32(maxRepeatedFailureResponses*3); got != want {
		t.Fatalf("executed = %d, want %d", got, want)
	}
}
