package interactive

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

// TestDriverRunCarriesLastMainRequest proves driverRun seeds the run with the
// driver's last main request and returns the run's request. A failed run that
// returns none keeps the seed; a request returned on error replaces it; a
// success takes the run's value, zero included. Each error case reports its
// stop reason once.
func TestDriverRunCarriesLastMainRequest(t *testing.T) {
	t.Parallel()
	seed := agent.LastMainRequest{At: time.Unix(100, 0), ProviderAlias: "openai", BackendModelID: "gpt-seed", PromptCacheKey: "cache-key"}
	returned := agent.LastMainRequest{At: time.Unix(200, 0), ProviderAlias: "openai", BackendModelID: "gpt-main", PromptCacheKey: "cache-key"}
	boom := errors.New("boom")
	tests := []struct {
		name string
		// runReq and runErr are what the run returns.
		runReq agent.LastMainRequest
		runErr error
		want   agent.LastMainRequest
	}{
		{name: "success adopts the run's request", runReq: returned, want: returned},
		{name: "success with zero resets", want: agent.LastMainRequest{}},
		{name: "success that keeps the seed", runReq: seed, want: seed},
		{name: "error with zero keeps the seed", runErr: boom, want: seed},
		{name: "cancel with zero keeps the seed", runErr: context.Canceled, want: seed},
		{name: "error with a request replaces the seed", runReq: returned, runErr: boom, want: returned},
		{name: "cancel with a request replaces the seed", runReq: returned, runErr: context.Canceled, want: returned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var events []output.Event
			s := testNewSession(t, Dependencies{
				BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
			})
			var got RunInput
			s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
				got = in
				return RunResult{Conversation: in.Conversation, LastMainRequest: tt.runReq}, tt.runErr
			}})

			out, err := s.driverRun(context.Background(), agent.DriverRunInput{
				Conversation:    []agent.Message{{Role: agent.MessageRoleUser, Content: "hi"}},
				LastMainRequest: seed,
			}, "")
			if err != nil {
				t.Fatalf("driverRun() error = %v", err)
			}
			if got.LastMainRequest != seed {
				t.Errorf("RunInput.LastMainRequest = %+v, want the driver's %+v", got.LastMainRequest, seed)
			}
			if out.LastMainRequest != tt.want {
				t.Errorf("DriverRunOutput.LastMainRequest = %+v, want %+v", out.LastMainRequest, tt.want)
			}

			wantStops := 0
			if tt.runErr != nil {
				wantStops = 1
			}
			if stops := eventsOfType(events, output.EventTypeStopReason); len(stops) != wantStops {
				t.Errorf("stop-reason events = %d, want %d", len(stops), wantStops)
			}
		})
	}
}
