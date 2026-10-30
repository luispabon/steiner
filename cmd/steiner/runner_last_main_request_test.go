package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// lastMainRequestSeedFor returns a last main request measured on the runner's
// resolved model and prompt cache key, so the run accepts it as its seed.
func lastMainRequestSeedFor(t *testing.T, r cliRunner, at time.Time) agent.LastMainRequest {
	t.Helper()
	setup, err := r.prepareRun(nil, nil)
	if err != nil {
		t.Fatalf("prepareRun() error = %v", err)
	}
	return agent.LastMainRequest{
		At:             at,
		ProviderAlias:  setup.resolvedModel.ProviderAlias,
		BackendModelID: setup.resolvedModel.BackendModelID,
		PromptCacheKey: r.promptCacheKey(),
	}
}

// TestSessionRunnerLastMainRequestRoundTrip proves the interactive seam carries
// the last main request in and out: RunInput.LastMainRequest seeds the run, and
// RunResult.LastMainRequest returns the run's request, on error as well.
func TestSessionRunnerLastMainRequestRoundTrip(t *testing.T) {
	seedAt := time.Unix(100, 0)
	reply := provider.ChatResponse{Message: provider.Message{Content: "reply"}, FinishReason: "stop"}
	tests := []struct {
		name string
		prov parentUsageProvider
		// wantSeed reports the run ends with the seed, because no main call succeeded.
		wantSeed bool
	}{
		{name: "success returns the main call", prov: parentUsageProvider{fakeProvider: &fakeProvider{}, resp: reply}},
		{name: "error keeps the seed", prov: parentUsageProvider{fakeProvider: &fakeProvider{}, err: errors.New("boom")}, wantSeed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := parentUsageTestRunner(t, tt.prov)
			seed := lastMainRequestSeedFor(t, runner, seedAt)

			result, err := sessionRunner{runner: runner}.Run(context.Background(), interactive.RunInput{
				Conversation:    []agent.Message{{Role: agent.MessageRoleUser, Content: "hi"}},
				LastMainRequest: seed,
			})
			if (err != nil) != (tt.prov.err != nil) {
				t.Fatalf("Run() error = %v, want error = %v", err, tt.prov.err != nil)
			}
			got := result.LastMainRequest
			if tt.wantSeed {
				if got != seed {
					t.Errorf("RunResult.LastMainRequest = %+v, want the seed %+v", got, seed)
				}
				return
			}
			if got.At.IsZero() || got.At.Equal(seedAt) {
				t.Errorf("RunResult.LastMainRequest.At = %v, want the time of the main call", got.At)
			}
			if got.ProviderAlias != seed.ProviderAlias || got.BackendModelID != seed.BackendModelID || got.PromptCacheKey != seed.PromptCacheKey {
				t.Errorf("RunResult.LastMainRequest = %+v, want the model identity of %+v", got, seed)
			}
		})
	}
}

// TestBuildRunRequestSeedsLastMainRequest proves the hook's last main request
// becomes the request's seed, and that a run without hooks has none.
func TestBuildRunRequestSeedsLastMainRequest(t *testing.T) {
	runner := parentUsageTestRunner(t, &fakeProvider{})
	setup, err := runner.prepareRun(nil, nil)
	if err != nil {
		t.Fatalf("prepareRun() error = %v", err)
	}
	seed := lastMainRequestSeedFor(t, runner, time.Unix(100, 0))

	if got := buildRunRequest(runner, setup, tool.NewRegistry(), nil, runHooks{lastMainRequest: seed}).SeedLastMainRequest; got != seed {
		t.Errorf("SeedLastMainRequest = %+v, want %+v", got, seed)
	}
	if got := buildRunRequest(runner, setup, tool.NewRegistry(), nil, runHooks{}).SeedLastMainRequest; got != (agent.LastMainRequest{}) {
		t.Errorf("SeedLastMainRequest without hooks = %+v, want zero", got)
	}
}

// TestRunWithHooksLastMainRequestRoundTrip proves the seed is returned when no
// main call succeeds, and that a successful main call is returned on success
// and when the run fails after it.
func TestRunWithHooksLastMainRequestRoundTrip(t *testing.T) {
	seedAt := time.Unix(100, 0)
	reply := provider.ChatResponse{
		Message:      provider.Message{Content: "reply"},
		FinishReason: "stop",
	}
	toolCall := provider.ChatResponse{
		Message: provider.Message{
			Role:      provider.MessageRoleAssistant,
			ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "probe"}},
		},
		FinishReason: "tool_calls",
	}
	tests := []struct {
		name      string
		responses []provider.ChatResponse
		// wantErr reports the run ends with a provider error.
		wantErr bool
		// wantCall reports the run ends with a main call it made, not the seed.
		wantCall bool
	}{
		{name: "success returns the main call", responses: []provider.ChatResponse{reply}, wantCall: true},
		{name: "failed first call keeps the seed", wantErr: true},
		{name: "error after a main call keeps that call", responses: []provider.ChatResponse{toolCall}, wantErr: true, wantCall: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov := &fakeProvider{responses: tt.responses}
			runner := parentUsageTestRunner(t, prov)
			seed := lastMainRequestSeedFor(t, runner, seedAt)
			conversation := []agent.Message{{Role: agent.MessageRoleUser, Content: "hi"}}

			result, err := runner.RunWithHooks(context.Background(), conversation, nil, runHooks{lastMainRequest: seed})
			if (err != nil) != tt.wantErr {
				t.Fatalf("RunWithHooks() error = %v, want error = %v", err, tt.wantErr)
			}
			got := result.LastMainRequest
			if !tt.wantCall {
				if got != seed {
					t.Errorf("LastMainRequest = %+v, want the seed %+v", got, seed)
				}
				return
			}
			if got.At.IsZero() || got.At.Equal(seedAt) {
				t.Errorf("LastMainRequest.At = %v, want the time of the main call", got.At)
			}
			if got.ProviderAlias != seed.ProviderAlias || got.BackendModelID != seed.BackendModelID || got.PromptCacheKey != seed.PromptCacheKey {
				t.Errorf("LastMainRequest = %+v, want the model identity of %+v", got, seed)
			}
		})
	}
}
