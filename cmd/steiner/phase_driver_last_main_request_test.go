package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
)

// TestRunPhaseOnDriverLastMainRequestAcrossEpisodes proves a phase carries its
// last main request from the first episode into the second. The second episode
// returns its own request on success, and the carried request on failure.
func TestRunPhaseOnDriverLastMainRequestAcrossEpisodes(t *testing.T) {
	reply := provider.ChatResponse{Message: provider.Message{Content: "reply"}, FinishReason: "stop"}
	tests := []struct {
		name      string
		responses []provider.ChatResponse
		// wantErr reports the second episode fails before any main call.
		wantErr bool
	}{
		{name: "second episode success returns its main call", responses: []provider.ChatResponse{reply, reply}},
		{name: "failed second episode keeps the first episode's request", responses: []provider.ChatResponse{reply}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov := &fakeProvider{responses: tt.responses}
			runner := parentUsageTestRunner(t, prov)
			h := &phaseHarness{bg: &phaseBackground{}}
			episode := runner.driverRun(nil, "", nil)
			episodes := 0
			var first agent.LastMainRequest
			host := h.host(func(ctx context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
				out, err := episode(ctx, in)
				episodes++
				if episodes == 1 {
					first = out.LastMainRequest
					// A completion delivered while the episode is in flight starts the second
					// episode at once. One that arrives while the driver waits would sit in
					// its coalescing window.
					h.bg.addPending("a")
					h.sink.DeliverCompletions([]agent.SubAgentCompletion{{Seq: 1, ParentCallID: "call-a", AgentID: "a", AgentType: "code", Status: "completed"}})
				}
				return out, err
			})

			ctx, cancel := context.WithTimeout(context.Background(), phaseDriverTestTimeout)
			defer cancel()
			result, err := runPhaseOnDriver(ctx, h.input(), host)
			if (err != nil) != tt.wantErr {
				t.Fatalf("runPhaseOnDriver() error = %v, want error = %v", err, tt.wantErr)
			}
			if episodes != 2 {
				t.Fatalf("episodes = %d, want 2", episodes)
			}
			if first.At.IsZero() {
				t.Fatal("first episode returned no main request")
			}

			final := result.LastMainRequest
			if tt.wantErr {
				if final != first {
					t.Errorf("RunResult.LastMainRequest = %+v, want the first episode's %+v", final, first)
				}
				return
			}
			if final.At.IsZero() || final.At.Before(first.At) {
				t.Errorf("RunResult.LastMainRequest.At = %v, want the second episode's main call at or after %v", final.At, first.At)
			}
			if final.ProviderAlias != first.ProviderAlias || final.BackendModelID != first.BackendModelID || final.PromptCacheKey != first.PromptCacheKey {
				t.Errorf("RunResult.LastMainRequest = %+v, want the model identity of %+v", final, first)
			}
		})
	}
}

// TestRunPhaseOnDriverResultCarriesLastMainRequest proves the phase result
// returns the driver's last main request on success and on failure. A phase
// that fails before any main call returns none.
func TestRunPhaseOnDriverResultCarriesLastMainRequest(t *testing.T) {
	req := agent.LastMainRequest{At: time.Unix(200, 0), ProviderAlias: "openai", BackendModelID: "gpt-test", PromptCacheKey: "cache-key"}
	boom := errors.New("provider down")
	tests := []struct {
		name    string
		out     agent.LastMainRequest
		err     error
		want    agent.LastMainRequest
		wantErr bool
	}{
		{name: "success returns the run's request", out: req, want: req},
		{name: "failure after a main call returns that request", out: req, err: boom, want: req, wantErr: true},
		{name: "early failure returns no request", err: boom, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &phaseHarness{bg: &phaseBackground{}}
			host := h.host(func(_ context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
				return agent.DriverRunOutput{Conversation: in.Conversation, LastMainRequest: tt.out}, tt.err
			})

			result, err := runPhaseOnDriver(context.Background(), h.input(), host)
			if (err != nil) != tt.wantErr {
				t.Fatalf("runPhaseOnDriver() error = %v, want error = %v", err, tt.wantErr)
			}
			if result.LastMainRequest != tt.want {
				t.Errorf("RunResult.LastMainRequest = %+v, want %+v", result.LastMainRequest, tt.want)
			}
		})
	}
}
