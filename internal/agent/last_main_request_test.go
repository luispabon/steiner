package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// steppingClock reads base, base+1s, base+2s, ... on successive Now calls, so
// a test can tell a request start time from a later reading.
type steppingClock struct {
	realClock
	base  time.Time
	reads int
}

func (c *steppingClock) Now() time.Time {
	at := c.base.Add(time.Duration(c.reads) * time.Second)
	c.reads++
	return at
}

// mainRequestRun returns a run request for the test model identity.
func mainRequestRun(prov provider.Provider, clock Clock) RunRequest {
	return RunRequest{
		Provider:       prov,
		Executor:       &fakeExecutor{},
		ResolvedModel:  provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-test"},
		PromptCacheKey: "cache-key",
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hi"}},
		},
		Clock: clock,
	}
}

func TestInitializeRunStateSeedsLastMainRequestOnIdentityMatch(t *testing.T) {
	t.Parallel()
	seed := LastMainRequest{
		At:             time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC),
		ProviderAlias:  "openai",
		BackendModelID: "gpt-test",
		PromptCacheKey: "cache-key",
	}

	tests := []struct {
		name   string
		seed   LastMainRequest
		mutate func(*RunRequest)
		want   LastMainRequest
	}{
		{name: "matching identity keeps the seed", seed: seed, want: seed},
		{name: "no seed starts with none", seed: LastMainRequest{}, want: LastMainRequest{}},
		{name: "provider alias mismatch drops the seed", seed: seed, mutate: func(r *RunRequest) { r.ResolvedModel.ProviderAlias = "other" }},
		{name: "backend model mismatch drops the seed", seed: seed, mutate: func(r *RunRequest) { r.ResolvedModel.BackendModelID = "other" }},
		{name: "prompt cache key mismatch drops the seed", seed: seed, mutate: func(r *RunRequest) { r.PromptCacheKey = "other" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := mainRequestRun(nil, nil)
			req.SeedLastMainRequest = tt.seed
			if tt.mutate != nil {
				tt.mutate(&req)
			}
			if got := initializeRunState(req).LastMainRequest; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("LastMainRequest = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRunRecordsLastMainRequestStartOnSuccessfulCall(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	prov := &fakeProvider{responses: []provider.ChatResponse{{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "ok"},
		FinishReason: "stop",
	}}}

	state, err := NewRunner().Run(context.Background(), mainRequestRun(prov, &steppingClock{base: start}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := LastMainRequest{At: start, ProviderAlias: "openai", BackendModelID: "gpt-test", PromptCacheKey: "cache-key"}
	if !reflect.DeepEqual(state.LastMainRequest, want) {
		t.Errorf("LastMainRequest = %+v, want %+v (request start, not completion)", state.LastMainRequest, want)
	}
}

func TestRunKeepsSeedWhenMainCallFails(t *testing.T) {
	t.Parallel()
	seed := LastMainRequest{
		At:             time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC),
		ProviderAlias:  "openai",
		BackendModelID: "gpt-test",
		PromptCacheKey: "cache-key",
	}
	prov := &fakeProvider{chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		return provider.ChatResponse{}, errors.New("upstream failed")
	}}
	req := mainRequestRun(prov, &steppingClock{base: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)})
	req.SeedLastMainRequest = seed

	state, err := NewRunner().Run(context.Background(), req)
	if err == nil {
		t.Fatal("Run() error = nil, want provider error")
	}
	if !reflect.DeepEqual(state.LastMainRequest, seed) {
		t.Errorf("LastMainRequest = %+v, want seed %+v after failed call", state.LastMainRequest, seed)
	}
}

func TestRunCompactionKeepsSeedWhenMainCallFails(t *testing.T) {
	t.Parallel()
	seed := LastMainRequest{
		At:             time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC),
		ProviderAlias:  "openai",
		BackendModelID: "gpt-test",
		PromptCacheKey: "cache-key",
	}
	// The first call is the compaction summary and succeeds; every later call
	// is a main model call and fails, so the run must keep the seed.
	calls := 0
	prov := &fakeProvider{chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		calls++
		if calls == 1 {
			return provider.ChatResponse{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "handoff"},
				FinishReason: "stop",
			}, nil
		}
		return provider.ChatResponse{}, errors.New("main call failed")
	}}
	req := mainRequestRun(prov, &steppingClock{base: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)})
	req.SeedLastMainRequest = seed
	req.ModelBudget = prompt.ModelTokenBudget{
		ContextSize:               3235,
		MaxCompletionTokens:       32,
		SummaryMaxTokens:          32,
		NormalSummaryMaxTokens:    32,
		EmergencySummaryMaxTokens: 16,
	}
	req.Prompt.Conversation = []provider.Message{
		{Role: provider.MessageRoleUser, Content: strings.Repeat("initial request ", 120)},
		{Role: provider.MessageRoleAssistant, Content: strings.Repeat("initial answer ", 96)},
		{Role: provider.MessageRoleUser, Content: strings.Repeat("second request ", 112)},
		{Role: provider.MessageRoleAssistant, Content: strings.Repeat("second answer ", 88)},
	}
	req.Limits = Limits{MaxTurns: 3}

	state, err := NewRunner().Run(context.Background(), req)
	if err == nil {
		t.Fatal("Run() error = nil, want main call failure")
	}
	if got := len(state.Lineage.Generations); got < 2 {
		t.Fatalf("lineage generations = %d, want a compaction generation", got)
	}
	if !reflect.DeepEqual(state.LastMainRequest, seed) {
		t.Errorf("LastMainRequest = %+v, want seed %+v: compaction must not record", state.LastMainRequest, seed)
	}
}
