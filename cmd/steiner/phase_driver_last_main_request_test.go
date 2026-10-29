package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
)

// TestRunPhaseOnDriverLastMainRequestAcrossEpisodes proves a phase carries its
// last main request from the first episode into the second. The second episode
// is seeded with the first's output and, on success, returns its own later call.
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
			var seeds, outputs []agent.LastMainRequest
			host := h.host(func(ctx context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
				if len(outputs) == 1 {
					// Wait until the clock is past the first call, so the second call's
					// request time is strictly later. Polling the monotonic clock avoids
					// depending on the timestamp resolution.
					for !time.Now().After(outputs[0].At) {
						if err := ctx.Err(); err != nil {
							return agent.DriverRunOutput{}, fmt.Errorf("wait for clock past first call: %w", err)
						}
						runtime.Gosched()
					}
				}
				seeds = append(seeds, in.LastMainRequest)
				out, err := episode(ctx, in)
				outputs = append(outputs, out.LastMainRequest)
				if len(outputs) == 1 {
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
			if len(seeds) != 2 || len(outputs) != 2 {
				t.Fatalf("episodes: %d seeds and %d outputs, want 2 of each", len(seeds), len(outputs))
			}
			if seeds[0] != (agent.LastMainRequest{}) {
				t.Errorf("first episode seed = %+v, want none in a cold phase", seeds[0])
			}
			first := outputs[0]
			if first.At.IsZero() {
				t.Fatal("first episode returned no main request")
			}
			if seeds[1] != first {
				t.Errorf("second episode seed = %+v, want the first episode's output %+v", seeds[1], first)
			}

			second := outputs[1]
			if tt.wantErr {
				// The failed episode made no main call, so it hands back its seed.
				if second != first {
					t.Errorf("failed second episode output = %+v, want its seed %+v", second, first)
				}
				if result.LastMainRequest != first {
					t.Errorf("RunResult.LastMainRequest = %+v, want the first episode's %+v", result.LastMainRequest, first)
				}
				return
			}
			if !second.At.After(first.At) {
				t.Errorf("second episode At = %v, want strictly after the first call at %v", second.At, first.At)
			}
			if result.LastMainRequest != second {
				t.Errorf("RunResult.LastMainRequest = %+v, want the second episode's output %+v", result.LastMainRequest, second)
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
