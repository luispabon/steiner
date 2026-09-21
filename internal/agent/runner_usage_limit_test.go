package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func usageLimitRunRequest(p provider.Provider, events *[]output.Event) RunRequest {
	return RunRequest{
		Provider:      p,
		Executor:      &fakeExecutor{},
		ResolvedModel: provider.ResolvedModel{Alias: "luna", ProviderAlias: "codex", ReasoningEffectiveEffort: "high"},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		Limits: Limits{MaxTurns: 6, MaxTokens: 100},
		Events: output.SinkFunc(func(event output.Event) { *events = append(*events, event) }),
	}
}

func TestRunnerUsageLimitStopsWithoutRetry(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "non-stream first"
		if streaming {
			name = "stream first"
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			ule := &provider.UsageLimitError{
				Kind:     provider.UsageLimitKindUsage,
				Provider: "codex",
				HTTP:     &provider.HTTPError{StatusCode: 429},
			}
			stub := &fakeProvider{
				chatFn: func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
					calls++
					return provider.ChatResponse{}, ule
				},
				streamFn: func(_ context.Context, _ provider.ChatRequest) (<-chan provider.ChatChunk, error) {
					calls++
					return nil, ule
				},
			}
			origSleep := runnerRetrySleepFn
			sleeps := 0
			runnerRetrySleepFn = func(_ context.Context, _ time.Duration) error { sleeps++; return nil }
			defer func() { runnerRetrySleepFn = origSleep }()

			var events []output.Event
			req := usageLimitRunRequest(stub, &events)
			req.StreamingPreferred = streaming
			state, err := NewRunner().Run(context.Background(), req)
			if err == nil {
				t.Fatal("Run() error = nil, want usage limit error")
			}
			if state.StopReason != StopReasonUsageLimit {
				t.Errorf("StopReason = %q, want %q", state.StopReason, StopReasonUsageLimit)
			}
			if calls != 1 || sleeps != 0 {
				t.Errorf("provider calls = %d (want exactly 1), runner retry sleeps = %d (want 0)", calls, sleeps)
			}
			if ule.Model != "codex/luna/high" {
				t.Errorf("ule.Model = %q, want codex/luna/high", ule.Model)
			}
			if !strings.Contains(err.Error(), "codex/luna/high") {
				t.Errorf("error %q does not name the model", err.Error())
			}
			var found bool
			for _, ev := range events {
				if p, ok := ev.Payload.(output.StopReasonEvent); ok && p.Reason == "usage_limit" {
					found = true
				}
			}
			if !found {
				t.Errorf("events = %#v, want usage_limit stop event", events)
			}
		})
	}
}

func TestRunnerPlainRateLimitStillRetried(t *testing.T) {
	calls := 0
	stub := &fakeProvider{
		chatFn: func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
			calls++
			return provider.ChatResponse{}, &provider.HTTPError{StatusCode: 429, Status: "429 Too Many Requests", Body: `{"error":"rate limited"}`}
		},
	}
	origSleep := runnerRetrySleepFn
	runnerRetrySleepFn = func(_ context.Context, _ time.Duration) error { return nil }
	defer func() { runnerRetrySleepFn = origSleep }()

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), usageLimitRunRequest(stub, &events))
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
	if calls <= 1 {
		t.Errorf("provider calls = %d, want retries (>1)", calls)
	}
	if state.StopReason == StopReasonUsageLimit {
		t.Errorf("StopReason = usage_limit for plain 429")
	}
}
