package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// parentUsageProvider answers every chat call with resp, or with err when set.
type parentUsageProvider struct {
	*fakeProvider
	resp provider.ChatResponse
	err  error
}

func (p parentUsageProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return p.resp, p.err
}

// parentUsageTestRunner returns an interactive cliRunner over prov.
func parentUsageTestRunner(t *testing.T, prov provider.Provider) cliRunner {
	t.Helper()
	return cliRunner{
		runtime: cliRuntime{
			cfg:      testRuntimeConfig("test-model"),
			provider: prov,
			registry: tool.NewRegistry(),
			workDir:  t.TempDir(),
			homeDir:  t.TempDir(),
			events:   output.NoopSink{},
		},
		runMode: "interactive",
	}
}

// parentUsageSeedFor returns parent usage measured on the runner's resolved
// model and context window, so the run accepts it as its seed.
func parentUsageSeedFor(t *testing.T, r cliRunner, promptTokens int) *agent.ParentUsage {
	t.Helper()
	setup, err := r.prepareRun(nil, nil)
	if err != nil {
		t.Fatalf("prepareRun() error = %v", err)
	}
	return &agent.ParentUsage{
		ModelBackendID: setup.resolvedModel.BackendModelID,
		ContextSize:    setup.modelBudget.ContextSize,
		Usage:          &provider.UsageStats{PromptTokens: promptTokens},
	}
}

// TestRunWithHooksParentUsageRoundTrip proves the seed reaches the run and the
// usage the run ends with is returned on success and on error.
func TestRunWithHooksParentUsageRoundTrip(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name    string
		callErr error
		// wantSeed reports the run ends with the seed, because no response arrived.
		wantSeed bool
	}{
		{name: "success returns the response usage"},
		{name: "error keeps the seeded usage", callErr: boom, wantSeed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov := parentUsageProvider{
				fakeProvider: &fakeProvider{},
				resp: provider.ChatResponse{
					Message:      provider.Message{Content: "reply"},
					FinishReason: "stop",
					Usage:        &provider.UsageStats{PromptTokens: 100},
				},
				err: tt.callErr,
			}
			runner := parentUsageTestRunner(t, prov)
			seed := parentUsageSeedFor(t, runner, 800)
			conversation := []agent.Message{{Role: agent.MessageRoleUser, Content: "hi"}}

			result, err := runner.RunWithHooks(context.Background(), conversation, nil, runHooks{parentUsage: seed})
			if (err != nil) != (tt.callErr != nil) {
				t.Fatalf("RunWithHooks() error = %v, want error = %v", err, tt.callErr != nil)
			}
			want := &agent.ParentUsage{ModelBackendID: seed.ModelBackendID, ContextSize: seed.ContextSize, Usage: &provider.UsageStats{PromptTokens: 100}}
			if tt.wantSeed {
				want = seed
			}
			if !reflect.DeepEqual(result.ParentUsage, want) {
				t.Errorf("result.ParentUsage = %+v, want %+v", result.ParentUsage, want)
			}
		})
	}
}

// TestSessionRunnerParentUsageRoundTrip proves the interactive seam carries the
// usage in and out: RunInput.ParentUsage seeds the run and RunResult.ParentUsage
// returns the run's usage, on error as well.
func TestSessionRunnerParentUsageRoundTrip(t *testing.T) {
	prov := parentUsageProvider{fakeProvider: &fakeProvider{}, err: errors.New("boom")}
	runner := parentUsageTestRunner(t, prov)
	seed := parentUsageSeedFor(t, runner, 800)

	result, err := sessionRunner{runner: runner}.Run(context.Background(), interactive.RunInput{
		Conversation: []agent.Message{{Role: agent.MessageRoleUser, Content: "hi"}},
		ParentUsage:  seed,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want the provider error")
	}
	if !reflect.DeepEqual(result.ParentUsage, seed) {
		t.Errorf("RunResult.ParentUsage = %+v, want the seed %+v", result.ParentUsage, seed)
	}
}

// TestBuildRunRequestSeedsParentUsage proves the hook's usage becomes the
// request's seed, and that a run without hooks has none.
func TestBuildRunRequestSeedsParentUsage(t *testing.T) {
	runner := parentUsageTestRunner(t, &fakeProvider{})
	setup, err := runner.prepareRun(nil, nil)
	if err != nil {
		t.Fatalf("prepareRun() error = %v", err)
	}
	seed := parentUsageSeedFor(t, runner, 800)

	if got := buildRunRequest(runner, setup, tool.NewRegistry(), nil, runHooks{parentUsage: seed}).SeedParentUsage; !reflect.DeepEqual(got, seed) {
		t.Errorf("SeedParentUsage = %+v, want %+v", got, seed)
	}
	if got := buildRunRequest(runner, setup, tool.NewRegistry(), nil, runHooks{}).SeedParentUsage; got != nil {
		t.Errorf("SeedParentUsage without hooks = %+v, want nil", got)
	}
}
