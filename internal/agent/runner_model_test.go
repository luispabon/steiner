package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestRunnerResetsTurnTimeoutEachTurn(t *testing.T) {
	const turnTimeout = 100 * time.Millisecond
	const turnDelay = 60 * time.Millisecond

	providerStub := &fakeProvider{}
	providerStub.chatFn = func(ctx context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
		callNum := len(providerStub.requests)
		select {
		case <-time.After(turnDelay):
		case <-ctx.Done():
			return provider.ChatResponse{}, ctx.Err()
		}

		switch callNum {
		case 1:
			return provider.ChatResponse{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			}, nil
		case 2:
			return provider.ChatResponse{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 3, CompletionTokens: 3},
			}, nil
		default:
			return provider.ChatResponse{}, fmt.Errorf("unexpected request %d", callNum)
		}
	}

	executor := &fakeExecutor{
		execute: func(_ context.Context, toolName string, _ map[string]any) (any, error) {
			if toolName != "read" {
				return nil, fmt.Errorf("tool = %s, want read", toolName)
			}
			return map[string]any{"contents": "hello"}, nil
		},
	}

	start := time.Now()
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "fix the bug"}},
		},
		Limits: Limits{MaxTurns: 4, MaxTokens: 50, TurnTimeout: turnTimeout},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 2; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
	if elapsed < 2*turnDelay {
		t.Fatalf("elapsed = %v, want at least %v for two completed turns", elapsed, 2*turnDelay)
	}
	if elapsed < turnTimeout {
		t.Fatalf("elapsed = %v, want to exceed a single turn timeout budget", elapsed)
	}
}

//nolint:gocyclo
func TestRunnerStreamsAssistantChunksBeforeFinalMessage(t *testing.T) {
	providerStub := &fakeProvider{
		streamFn: func(_ context.Context, _ provider.ChatRequest) (<-chan provider.ChatChunk, error) {
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
					Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
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
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		MaxTokens:     intPtr(32),
		Limits:        Limits{MaxTurns: 2, MaxTokens: 100},
		Events:        output.NoopSink{},
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

	if got, want := state.TokenCount, 0; got != want {
		t.Fatalf("TokenCount = %d, want %d (no usage stats reported, only completion tokens count)", got, want)
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
				Usage:        &provider.UsageStats{TotalTokens: 1, CompletionTokens: 1},
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
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		MaxTokens:     intPtr(32),
		Limits:        Limits{MaxTurns: 2, MaxTokens: 100},
		Events:        output.NoopSink{},
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
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
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

func TestRunnerStopsAtMaxTokens(t *testing.T) {
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
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return map[string]any{"contents": "hello"}, nil
		},
	}

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		Limits: Limits{MaxTurns: 4, MaxTokens: 5},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if got, want := state.StopReason, StopReasonMaxTokens; got != want {
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
	providerStub.chatFn = func(ctx context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
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
		payload, ok := output.AsContextBudgetEvent(event.Payload)
		if !ok {
			t.Fatalf("diagnostic payload type = %T, want output.ContextBudgetEvent", event.Payload)
		}
		if payload.ContextWindow == 4096 {
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

// TestRunnerDetectedReasoningEchoBack_PersistsAcrossTurns verifies that when
// the model returns reasoning_content on turn 1 with ReasoningEchoBack=false,
// the runner enables ReasoningEchoBack for turn 2 so reasoning is preserved.
func TestRunnerRetriesTransientProviderError(t *testing.T) {
	// Each turn attempt may call ChatCompletion twice (initial + fallback after
	// streaming fails), so we need enough failures to span 2 full turn attempts.
	callCount := 0
	providerStub := &fakeProvider{
		chatFn: func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
			callCount++
			if callCount <= 4 {
				return provider.ChatResponse{}, &provider.HTTPError{
					StatusCode: 429,
					Status:     "429 Too Many Requests",
					Body:       `{"error":"rate limited"}`,
				}
			}
			return provider.ChatResponse{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
			}, nil
		},
	}

	sleepCalls := 0
	origSleep := runnerRetrySleepFn
	runnerRetrySleepFn = func(_ context.Context, _ time.Duration) error {
		sleepCalls++
		return nil
	}
	defer func() { runnerRetrySleepFn = origSleep }()

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		Limits: Limits{MaxTurns: 6, MaxTokens: 100},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if sleepCalls != 2 {
		t.Fatalf("runner retry sleeps = %d, want 2", sleepCalls)
	}

	var diagnosticCount int
	for _, ev := range events {
		if ev.Type == output.EventTypeProviderDiagnostic {
			diagnosticCount++
		}
	}
	if diagnosticCount != 2 {
		t.Fatalf("provider diagnostic events = %d, want 2", diagnosticCount)
	}

	var errorStopCount int
	for _, ev := range events {
		if ev.Type == output.EventTypeStopReason {
			if payload, ok := ev.Payload.(output.StopReasonEvent); ok && payload.Error != "" {
				errorStopCount++
			}
		}
	}
	if errorStopCount != 0 {
		t.Fatalf("error stop events during retries = %d, want 0", errorStopCount)
	}
}

func TestRunnerStopsAfterMaxRunnerRetries(t *testing.T) {
	providerStub := &fakeProvider{
		chatFn: func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{}, &provider.HTTPError{
				StatusCode: 429,
				Status:     "429 Too Many Requests",
				Body:       `{"error":"rate limited"}`,
			}
		},
	}

	origSleep := runnerRetrySleepFn
	runnerRetrySleepFn = func(_ context.Context, _ time.Duration) error {
		return nil
	}
	defer func() { runnerRetrySleepFn = origSleep }()

	var events []output.Event
	_, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		Limits: Limits{MaxTurns: 10, MaxTokens: 100},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err == nil {
		t.Fatal("Run() error = nil, want error after runner retries exhausted")
	}
	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Run() error type = %T, want *provider.HTTPError", err)
	}
	if httpErr.StatusCode != 429 {
		t.Fatalf("HTTPError.StatusCode = %d, want 429", httpErr.StatusCode)
	}

	var errorStopCount int
	for _, ev := range events {
		if ev.Type == output.EventTypeStopReason {
			if payload, ok := ev.Payload.(output.StopReasonEvent); ok && payload.Error != "" {
				errorStopCount++
			}
		}
	}
	if errorStopCount != 1 {
		t.Fatalf("error stop events = %d, want exactly 1 (only when runner gives up)", errorStopCount)
	}
}

func TestRunnerDoesNotRetryNonTransientErrors(t *testing.T) {
	providerStub := &fakeProvider{
		chatFn: func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{}, &provider.HTTPError{
				StatusCode: 400,
				Status:     "400 Bad Request",
				Body:       `{"error":"bad request"}`,
			}
		},
	}

	_, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		Limits: Limits{MaxTurns: 4, MaxTokens: 100},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Run() error type = %T, want *provider.HTTPError", err)
	}
	if httpErr.StatusCode != 400 {
		t.Fatalf("HTTPError.StatusCode = %d, want 400", httpErr.StatusCode)
	}
}

func TestRunnerResetsRetryCounterOnSuccess(t *testing.T) {
	callCount := 0
	providerStub := &fakeProvider{
		chatFn: func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
			callCount++
			switch callCount {
			case 1:
				return provider.ChatResponse{}, &provider.HTTPError{
					StatusCode: 502,
					Status:     "502 Bad Gateway",
				}
			case 2:
				return provider.ChatResponse{
					Message: provider.Message{
						Role: provider.MessageRoleAssistant,
						ToolCalls: []provider.ToolCall{
							{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
						},
					},
					FinishReason: "tool_calls",
					Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
				}, nil
			case 3:
				return provider.ChatResponse{}, &provider.HTTPError{
					StatusCode: 502,
					Status:     "502 Bad Gateway",
				}
			default:
				return provider.ChatResponse{
					Message: provider.Message{
						Role:    provider.MessageRoleAssistant,
						Content: "done",
					},
					FinishReason: "stop",
					Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
				}, nil
			}
		},
	}

	origSleep := runnerRetrySleepFn
	runnerRetrySleepFn = func(_ context.Context, _ time.Duration) error {
		return nil
	}
	defer func() { runnerRetrySleepFn = origSleep }()

	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return map[string]any{"contents": "hello"}, nil
		},
	}

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		Limits: Limits{MaxTurns: 6, MaxTokens: 100},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
}

func TestRunnerRateLimitGetsMoreRetries(t *testing.T) {
	callCount := 0
	providerStub := &fakeProvider{
		chatFn: func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
			callCount++
			return provider.ChatResponse{}, &provider.HTTPError{
				StatusCode: 429,
				Status:     "429 Too Many Requests",
				Body:       `{"error":{"message":"Try again in 5 seconds."}}`,
			}
		},
	}

	origSleep := runnerRetrySleepFn
	runnerRetrySleepFn = func(_ context.Context, _ time.Duration) error { return nil }
	defer func() { runnerRetrySleepFn = origSleep }()

	_, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		Limits: Limits{MaxTurns: 20, MaxTokens: 100},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
	// With maxRunnerRateLimitRetries=10, we expect 11 calls (initial + 10 retries).
	// Each call goes through provider-level retry which is 1 attempt here (no retry config).
	if callCount < maxRunnerRateLimitRetries+1 {
		t.Fatalf("callCount = %d, want at least %d (rate limit should get more retries)", callCount, maxRunnerRateLimitRetries+1)
	}
}

func TestRunnerServerErrorCapsAtDefaultRetries(t *testing.T) {
	callCount := 0
	providerStub := &fakeProvider{
		chatFn: func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
			callCount++
			return provider.ChatResponse{}, &provider.HTTPError{
				StatusCode: 502,
				Status:     "502 Bad Gateway",
			}
		},
	}

	origSleep := runnerRetrySleepFn
	runnerRetrySleepFn = func(_ context.Context, _ time.Duration) error { return nil }
	defer func() { runnerRetrySleepFn = origSleep }()

	_, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		},
		Limits: Limits{MaxTurns: 20, MaxTokens: 100},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
	// 429 rate-limit test above requires at least maxRunnerRateLimitRetries+1 calls.
	// 502 server errors must produce strictly fewer calls, confirming the lower cap.
	rateLimitMin := maxRunnerRateLimitRetries + 1
	if callCount >= rateLimitMin {
		t.Fatalf("callCount = %d, want less than %d (server error should cap below rate-limit budget)", callCount, rateLimitMin)
	}
}

func TestRunnerDetectedReasoningEchoBack_PersistsAcrossTurns(t *testing.T) {
	// Turn 1: model responds with reasoning_content and a tool call.
	// Turn 2: model responds with a final answer.
	// We assert that the turn-2 request preserved reasoning in the conversation.
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:             provider.MessageRoleAssistant,
					ReasoningContent: "thinking about the task",
					ToolCalls: []provider.ToolCall{
						{
							ID:        "call_1",
							Name:      "bash",
							Arguments: map[string]any{"command": "echo hi"},
						},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 10, CompletionTokens: 10},
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
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return map[string]any{"output": "hi"}, nil
		},
	}

	runner := NewRunner()
	_, err := runner.Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Tools: []provider.ToolSpec{
			{Type: "function", Function: provider.ToolFunctionSpec{Name: "bash", Description: "run shell", Parameters: map[string]any{"type": "object"}}},
		},
		Prompt: prompt.AssemblyOptions{
			Conversation:              []provider.Message{{Role: provider.MessageRoleUser, Content: "run something"}},
			ProjectContextBudgetBytes: 128,
		},
		ResolvedModel: provider.ResolvedModel{
			BackendModelID:    "test-model",
			ReasoningEchoBack: false, // starts false — runner must flip it after turn 1
		},
		MaxTokens:   intPtr(256),
		ModelBudget: prompt.ModelTokenBudget{ContextSize: 4096, MaxCompletionTokens: 256},
		Limits:      Limits{MaxTurns: 4, MaxTokens: 500},
		Events:      output.NoopSink{},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(providerStub.requests) < 2 {
		t.Fatalf("expected at least 2 provider requests, got %d", len(providerStub.requests))
	}

	// The second request must include the assistant message from turn 1 with
	// reasoning_content preserved (not stripped).
	turn2Req := providerStub.requests[1]
	var foundReasoning bool
	for _, msg := range turn2Req.Messages {
		if msg.Role == provider.MessageRoleAssistant && msg.ReasoningContent != "" {
			foundReasoning = true
			break
		}
	}
	if !foundReasoning {
		t.Fatal("turn-2 request missing reasoning_content in assistant message; ReasoningEchoBack was not applied")
	}
}
