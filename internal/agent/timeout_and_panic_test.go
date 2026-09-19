package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func timeoutTestRequest(p provider.Provider, limits Limits) RunRequest {
	return RunRequest{
		Provider: p,
		Executor: &fakeExecutor{},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hi"}},
		},
		Limits: limits,
		Events: output.NoopSink{},
	}
}

func stubRetrySleep(t *testing.T) {
	t.Helper()
	orig := runnerRetrySleepFn
	runnerRetrySleepFn = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { runnerRetrySleepFn = orig })
}

func TestRunnerRetriesModelCallTimeout(t *testing.T) {
	stubRetrySleep(t)
	attempts := 0
	providerStub := &fakeProvider{chatFn: func(ctx context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
		attempts++
		if attempts == 1 {
			<-ctx.Done()
			return provider.ChatResponse{}, ctx.Err()
		}
		return provider.ChatResponse{
			Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "done"},
			FinishReason: "stop",
			Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
		}, nil
	}}
	state, err := NewRunner().Run(context.Background(), timeoutTestRequest(providerStub,
		Limits{MaxTurns: 4, MaxTokens: 50, ModelCallTimeout: 20 * time.Millisecond}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if state.StopReason == StopReasonCancelled {
		t.Fatalf("StopReason = cancelled, want a timeout to not look like cancellation")
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (timeout retried once)", attempts)
	}
}

func TestRunnerModelCallTimeoutExhaustsRetriesWithError(t *testing.T) {
	stubRetrySleep(t)
	providerStub := &fakeProvider{chatFn: func(ctx context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
		<-ctx.Done()
		return provider.ChatResponse{}, ctx.Err()
	}}
	state, err := NewRunner().Run(context.Background(), timeoutTestRequest(providerStub,
		Limits{MaxTurns: 4, MaxTokens: 50, ModelCallTimeout: 5 * time.Millisecond}))
	if err == nil {
		t.Fatal("Run() error = nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want it to mention timeout", err)
	}
	if state.StopReason != StopReasonError {
		t.Fatalf("StopReason = %q, want %q", state.StopReason, StopReasonError)
	}
}

func TestRunnerTurnTimeoutIsNotCancellation(t *testing.T) {
	stubRetrySleep(t)
	providerStub := &fakeProvider{chatFn: func(ctx context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
		<-ctx.Done()
		return provider.ChatResponse{}, ctx.Err()
	}}
	state, err := NewRunner().Run(context.Background(), timeoutTestRequest(providerStub,
		Limits{MaxTurns: 4, MaxTokens: 50, TurnTimeout: 5 * time.Millisecond}))
	if err == nil {
		t.Fatal("Run() error = nil, want a turn timeout error")
	}
	if state.StopReason == StopReasonCancelled {
		t.Fatal("StopReason = cancelled, want turn timeout to surface as error")
	}
}

func TestRunnerParentCancellationStillCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	providerStub := &fakeProvider{chatFn: func(ctx context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
		cancel()
		<-ctx.Done()
		return provider.ChatResponse{}, ctx.Err()
	}}
	state, err := NewRunner().Run(ctx, timeoutTestRequest(providerStub,
		Limits{MaxTurns: 4, MaxTokens: 50, ModelCallTimeout: time.Minute, TurnTimeout: time.Minute}))
	if err != nil {
		t.Fatalf("Run() error = %v, want nil on cancellation", err)
	}
	if got, want := state.StopReason, StopReasonCancelled; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
}

func TestLimitTimeoutClassification(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	timedOut, cancelT := context.WithTimeoutCause(context.Background(), time.Nanosecond, errModelCallTimeout)
	defer cancelT()
	<-timedOut.Done()
	// Parent cancelled before the child deadline: the cause is the parent's.
	child, cancelC := context.WithTimeoutCause(parent, time.Hour, errModelCallTimeout)
	defer cancelC()

	tests := []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{"live", context.Background(), false},
		{"model call deadline", timedOut, true},
		{"parent cancel under child deadline", child, false},
		{"plain deadline", func() context.Context {
			c, cf := context.WithTimeout(context.Background(), time.Nanosecond)
			t.Cleanup(cf)
			<-c.Done()
			return c
		}(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLimitTimeout(tt.ctx); got != tt.want {
				t.Fatalf("isLimitTimeout = %v, want %v", got, tt.want)
			}
		})
	}
	if _, ok := provider.RetryableProviderError(limitTimeoutError(timedOut, time.Second)); !ok {
		t.Fatal("limitTimeoutError is not classified as transient")
	}
	if !errors.Is(context.Cause(timedOut), errModelCallTimeout) {
		t.Fatal("cause lost")
	}
}

func TestToolPanicIsolated(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		name := "serial"
		if parallel {
			name = "parallel"
		}
		t.Run(name, func(t *testing.T) {
			var events []output.Event
			var mu sync.Mutex
			req := RunRequest{
				Executor: parallelTestExecutor{fn: func(_ context.Context, name string) (any, error) {
					if name == "boom" {
						panic("kaboom")
					}
					return "ok-" + name, nil
				}},
				MaxParallelTools: 2,
				Events: output.SinkFunc(func(e output.Event) {
					mu.Lock()
					defer mu.Unlock()
					events = append(events, e)
				}),
			}
			if parallel {
				req.ParallelClassOf = func(string) ParallelClass { return ParallelClassTool }
			}
			p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)
			state := cancelDrainState("a", "boom", "c")
			outcome := p.executeToolCalls(context.Background(), state, parallelCalls("a", "boom", "c"))
			if outcome.Stop || outcome.Error != nil {
				t.Fatalf("outcome = %+v, want run to continue", outcome)
			}
			msgs := cancelDrainToolMessages(outcome.State.Conversation)
			if len(msgs) != 3 {
				t.Fatalf("tool results = %d, want exactly one per call (3)", len(msgs))
			}
			for i, id := range []string{"a", "boom", "c"} {
				if msgs[i].ToolCallID != id {
					t.Fatalf("result %d id = %q, want %q", i, msgs[i].ToolCallID, id)
				}
			}
			if !strings.Contains(msgs[1].Content, "panicked") || !strings.Contains(msgs[1].Content, "kaboom") {
				t.Fatalf("panic result = %q, want error mentioning panic", msgs[1].Content)
			}
			if msgs[0].Content != "ok-a" || msgs[2].Content != "ok-c" {
				t.Fatalf("sibling results = %q / %q, want untouched", msgs[0].Content, msgs[2].Content)
			}
			var sawStack bool
			for _, e := range events {
				if e.Type == output.EventTypeProviderDiagnostic {
					if d, ok := e.Payload.(output.ProviderDiagnosticEvent); ok && strings.Contains(d.Message, "goroutine") {
						sawStack = true
					}
				}
			}
			if !sawStack {
				t.Fatal("no diagnostic event carrying the stack trace")
			}
		})
	}
}

func TestTurnTimeoutDuringToolsStopsWithError(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		name := "serial"
		if parallel {
			name = "parallel"
		}
		t.Run(name, func(t *testing.T) {
			req := RunRequest{
				Executor: parallelTestExecutor{fn: func(ctx context.Context, name string) (any, error) {
					if name == "slow" {
						<-ctx.Done()
						return nil, ctx.Err()
					}
					return "ok-" + name, nil
				}},
				MaxParallelTools: 2,
				Limits:           Limits{TurnTimeout: 20 * time.Millisecond},
			}
			if parallel {
				req.ParallelClassOf = func(string) ParallelClass { return ParallelClassTool }
			}
			p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)
			ctx, cancel := context.WithTimeoutCause(context.Background(), 20*time.Millisecond, errTurnTimeout)
			defer cancel()
			outcome := p.executeToolCalls(ctx, cancelDrainState("slow", "b", "c"), parallelCalls("slow", "b", "c"))
			if !outcome.Stop || outcome.Error == nil {
				t.Fatalf("outcome = stop:%v err:%v, want stop with a timeout error", outcome.Stop, outcome.Error)
			}
			if outcome.State.StopReason != StopReasonError {
				t.Fatalf("StopReason = %q, want %q", outcome.State.StopReason, StopReasonError)
			}
			if got := len(cancelDrainToolMessages(outcome.State.Conversation)); got != 3 {
				t.Fatalf("tool results = %d, want exactly one per call (3)", got)
			}
		})
	}
}
