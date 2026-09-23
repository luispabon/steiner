package main

import (
	"context"
	"testing"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// TestBuildRunRequestSharesSessionCacheBaseline proves that every run request
// built from one session-scoped cliRunner carries the same baseline store, so
// successive prompts of one session compare against each other.
func TestBuildRunRequestSharesSessionCacheBaseline(t *testing.T) {
	store := agent.NewCacheBaselineStore()
	runner := cliRunner{runtime: cliRuntime{}, cacheBaseline: store}
	setup := runnerSetup{resolvedModel: provider.ResolvedModel{BackendModelID: "model"}}
	registry := tool.NewRegistry()

	first := buildRunRequest(runner, setup, registry, nil, nil)
	second := buildRunRequest(runner, setup, registry, nil, nil)
	if first.CacheBaseline != store {
		t.Fatalf("first run CacheBaseline = %p, want session store %p", first.CacheBaseline, store)
	}
	if second.CacheBaseline != store {
		t.Fatalf("second run CacheBaseline = %p, want session store %p", second.CacheBaseline, store)
	}
}

// TestBuildRunRequestCacheBaselineIsPerConstructionScope proves that distinct
// construction scopes (interactive session vs one exec invocation) do not
// accidentally reuse one baseline store.
func TestBuildRunRequestCacheBaselineIsPerConstructionScope(t *testing.T) {
	setup := runnerSetup{resolvedModel: provider.ResolvedModel{BackendModelID: "model"}}
	registry := tool.NewRegistry()

	interactive := cliRunner{runtime: cliRuntime{}, cacheBaseline: agent.NewCacheBaselineStore()}
	exec := cliRunner{runtime: cliRuntime{}, cacheBaseline: agent.NewCacheBaselineStore()}

	interactiveReq := buildRunRequest(interactive, setup, registry, nil, nil)
	execReq := buildRunRequest(exec, setup, registry, nil, nil)
	if interactiveReq.CacheBaseline == nil || execReq.CacheBaseline == nil {
		t.Fatalf("CacheBaseline = %p/%p, want non-nil stores", interactiveReq.CacheBaseline, execReq.CacheBaseline)
	}
	if interactiveReq.CacheBaseline == execReq.CacheBaseline {
		t.Fatalf("distinct construction scopes share baseline store %p", interactiveReq.CacheBaseline)
	}
}

// TestPhaseRunnerFactorySharesCacheBaselineAcrossPhases proves sequential
// phases of one oneshot execution share a single baseline store despite each
// phase building its own cliRunner.
func TestPhaseRunnerFactorySharesCacheBaselineAcrossPhases(t *testing.T) {
	store := agent.NewCacheBaselineStore()
	factory := phaseRunnerFactory{rootDir: t.TempDir(), baseline: store}

	first, err := factory.phaseParams(oneshot.PhasePlan, "alias", nil, config.AdvisorConfig{})
	if err != nil {
		t.Fatalf("phaseParams(plan) error = %v", err)
	}
	second, err := factory.phaseParams(oneshot.PhaseReview, "alias", nil, config.AdvisorConfig{})
	if err != nil {
		t.Fatalf("phaseParams(review) error = %v", err)
	}
	if first.CacheBaseline != store || second.CacheBaseline != store {
		t.Fatalf("phase params baseline = %p/%p, want factory store %p", first.CacheBaseline, second.CacheBaseline, store)
	}
}

// TestNewPhaseRunnerPropagatesCacheBaseline proves the factory's store reaches
// the phase cliRunner that actually builds run requests.
func TestNewPhaseRunnerPropagatesCacheBaseline(t *testing.T) {
	orig := buildPhaseRuntime
	buildPhaseRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags, _, _, _ string) (cliRuntime, error) {
		return cliRuntime{}, nil
	}
	t.Cleanup(func() { buildPhaseRuntime = orig })

	store := agent.NewCacheBaselineStore()
	runner, err := newPhaseRunner(context.Background(), nil, &cliFlags{}, phaseRunnerParams{CacheBaseline: store})
	if err != nil {
		t.Fatalf("newPhaseRunner() error = %v", err)
	}
	got, ok := runner.(phaseRunner)
	if !ok {
		t.Fatalf("newPhaseRunner() = %T, want phaseRunner", runner)
	}
	if got.runner.cacheBaseline != store {
		t.Fatalf("phase runner cacheBaseline = %p, want %p", got.runner.cacheBaseline, store)
	}
}
