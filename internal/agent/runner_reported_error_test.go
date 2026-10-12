package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// TestRunnerTerminalErrorIsReportedOnce covers both terminal error returns in
// runner.go. Each must emit one error stop event and return a ReportedError
// that unwraps to the provider error, not to the sleep error.
func TestRunnerTerminalErrorIsReportedOnce(t *testing.T) {
	tests := []struct {
		name       string
		provErr    *provider.HTTPError
		sleepErr   error
		wantSleeps int
	}{
		// Non-transient error: returned directly without a retry sleep.
		{
			name:    "non-transient",
			provErr: &provider.HTTPError{StatusCode: 400, Status: "400 Bad Request", Body: `{"error":"bad request"}`},
		},
		// Transient error whose retry sleep fails while the context is still
		// live: the retryErr path returns the provider error.
		{
			name:       "retry sleep fails",
			provErr:    &provider.HTTPError{StatusCode: 429, Status: "429 Too Many Requests", Body: `{"error":"rate limited"}`},
			sleepErr:   errors.New("sleep interrupted"),
			wantSleeps: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sleepCalls int
			origSleep := runnerRetrySleepFn
			runnerRetrySleepFn = func(_ context.Context, _ time.Duration) error {
				sleepCalls++
				return tc.sleepErr
			}
			t.Cleanup(func() { runnerRetrySleepFn = origSleep })

			providerStub := &fakeProvider{
				chatFn: func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
					return provider.ChatResponse{}, tc.provErr
				},
			}

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
			var reported *ReportedError
			if !errors.As(err, &reported) {
				t.Fatalf("Run() error = %T, want *ReportedError", err)
			}
			if !errors.Is(err, tc.provErr) {
				t.Fatalf("Run() error does not unwrap to provider error: %v", err)
			}
			if tc.sleepErr != nil && errors.Is(err, tc.sleepErr) {
				t.Fatalf("Run() error unwraps to sleep error, want provider error: %v", err)
			}
			if sleepCalls != tc.wantSleeps {
				t.Fatalf("retry sleeps = %d, want %d", sleepCalls, tc.wantSleeps)
			}

			var errorStops []output.StopReasonEvent
			for _, ev := range events {
				if ev.Type == output.EventTypeStopReason {
					if payload, ok := ev.Payload.(output.StopReasonEvent); ok && payload.Error != "" {
						errorStops = append(errorStops, payload)
					}
				}
			}
			if len(errorStops) != 1 {
				t.Fatalf("error stop events = %d, want 1", len(errorStops))
			}
			if got, want := errorStops[0].Error, tc.provErr.Error(); got != want {
				t.Fatalf("stop event error = %q, want %q", got, want)
			}
		})
	}
}
