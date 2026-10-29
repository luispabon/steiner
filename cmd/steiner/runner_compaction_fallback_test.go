package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// compactionFallbackModelConfig returns a model definition on providerAlias with
// the limits testRuntimeConfig gives its model.
func compactionFallbackModelConfig(providerAlias, id string) config.ModelConfig {
	return config.ModelConfig{
		Provider: providerAlias,
		ID:       id,
		Advanced: config.AdvancedConfig{
			Limits: config.AdvancedLimitsConfig{MaxOutputTokens: 64, ContextWindow: 4096},
		},
	}
}

// compactionFallbackRuntimeConfig returns a config whose main model is
// main-model on the local provider, which has a 5 minute cold-cache delay.
// fallback-model and other-model are on the same provider, and claude-model is
// on a claude_subscription provider. ref is the static compaction_fallback.
func compactionFallbackRuntimeConfig(ref string) config.Config {
	cfg := testRuntimeConfig("main-model")
	cfg.Providers = map[string]config.ProviderConfig{
		"local": {
			Type:                    config.ProviderTypeOpenAICompat,
			BaseURL:                 "http://localhost:11434/v1",
			CompactionFallbackAfter: config.MustDuration("5m"),
		},
		"claude": {Type: config.ProviderTypeClaudeSubscription},
	}
	cfg.Models.Definitions = map[string]config.ModelConfig{
		"main-model":     compactionFallbackModelConfig("local", "main-model"),
		"fallback-model": compactionFallbackModelConfig("local", "fallback-model"),
		"other-model":    compactionFallbackModelConfig("local", "other-model"),
		"claude-model":   compactionFallbackModelConfig("claude", "claude-model"),
	}
	cfg.Models.Effective.CompactionFallback = ref
	return cfg
}

// fallbackBuildRecorder is a provider factory that records each build and
// returns a fresh provider.
type fallbackBuildRecorder struct {
	models     []provider.ResolvedModel
	sessionIDs []string
}

func (rec *fallbackBuildRecorder) factory(rm provider.ResolvedModel, sessionID string) (provider.Provider, error) {
	rec.models = append(rec.models, rm)
	rec.sessionIDs = append(rec.sessionIDs, sessionID)
	return &fakeProvider{}, nil
}

// compactionFallbackRunner returns a cliRunner with its memo owner set, as the
// production constructors do. effective is the live assignments getter or nil.
func compactionFallbackRunner(cfg config.Config, factory func(provider.ResolvedModel, string) (provider.Provider, error), effective func() config.EffectiveModelAssignments) cliRunner {
	return cliRunner{
		runtime:           cliRuntime{cfg: cfg, providerFactory: factory},
		currentEffective:  effective,
		sessionIDFn:       func() string { return "session-1" },
		promptCacheKeyFn:  func() string { return "cache-key-1" },
		compactionTargets: delegation.NewCompactionFallbackTargets(),
	}
}

// compactionFallbackSetup resolves main-model from r's config as prepareRun does.
func compactionFallbackSetup(t *testing.T, r cliRunner) runnerSetup {
	t.Helper()
	rm, err := r.runtime.resolveModel("main-model")
	if err != nil {
		t.Fatalf("resolveModel(main-model) error = %v", err)
	}
	return runnerSetup{resolvedModel: rm}
}

func TestBuildRunRequestCompactionFallbackDisabled(t *testing.T) {
	tests := []struct {
		name   string
		config func(*config.Config)
		memo   bool
	}{
		{
			name:   "no fallback ref",
			config: func(cfg *config.Config) { cfg.Models.Effective.CompactionFallback = "" },
			memo:   true,
		},
		{
			name: "zero cold-cache delay",
			config: func(cfg *config.Config) {
				cfg.Providers["local"] = config.ProviderConfig{Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"}
			},
			memo: true,
		},
		{
			name:   "no memo owner",
			config: func(*config.Config) {},
			memo:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &fallbackBuildRecorder{}
			cfg := compactionFallbackRuntimeConfig("fallback-model")
			tc.config(&cfg)
			r := compactionFallbackRunner(cfg, rec.factory, nil)
			if !tc.memo {
				r.compactionTargets = nil
			}

			req := buildRunRequest(r, compactionFallbackSetup(t, r), tool.NewRegistry(), nil, runHooks{})
			if req.CompactionFallback.Resolve != nil || req.CompactionFallback.After != 0 {
				t.Fatalf("CompactionFallback = {After: %v, Ref: %q, Resolve set: %t}, want disabled", req.CompactionFallback.After, req.CompactionFallback.Ref, req.CompactionFallback.Resolve != nil)
			}
			if len(rec.models) != 0 {
				t.Fatalf("provider builds = %d, want 0", len(rec.models))
			}
		})
	}
}

func TestBuildRunRequestCompactionFallbackResolvesLazily(t *testing.T) {
	rec := &fallbackBuildRecorder{}
	r := compactionFallbackRunner(compactionFallbackRuntimeConfig("fallback-model"), rec.factory, nil)

	req := buildRunRequest(r, compactionFallbackSetup(t, r), tool.NewRegistry(), nil, runHooks{})
	if req.CompactionFallback.Resolve == nil {
		t.Fatal("CompactionFallback.Resolve = nil, want enabled fallback")
	}
	if req.CompactionFallback.Ref != "fallback-model" || req.CompactionFallback.After != 5*time.Minute {
		t.Fatalf("CompactionFallback Ref, After = %q, %v; want fallback-model, 5m0s", req.CompactionFallback.Ref, req.CompactionFallback.After)
	}
	if len(rec.models) != 0 {
		t.Fatalf("provider builds before Resolve = %d, want 0", len(rec.models))
	}

	target, err := req.CompactionFallback.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got := target.ResolvedModel.BackendModelID; got != "fallback-model" {
		t.Fatalf("resolved backend model = %q, want fallback-model", got)
	}
	if target.Provider == nil {
		t.Fatal("Resolve() provider = nil, want built provider")
	}
	if len(rec.sessionIDs) != 1 || rec.sessionIDs[0] != "session-1" {
		t.Fatalf("factory session IDs = %v, want [session-1]", rec.sessionIDs)
	}
}

func TestBuildRunRequestCompactionFallbackFollowsLiveAssignments(t *testing.T) {
	rec := &fallbackBuildRecorder{}
	live := "fallback-model"
	effective := func() config.EffectiveModelAssignments {
		return config.EffectiveModelAssignments{CompactionFallback: live}
	}
	// The static config has no fallback, so only the live assignments enable it.
	r := compactionFallbackRunner(compactionFallbackRuntimeConfig(""), rec.factory, effective)

	steps := []struct {
		ref       string
		wantModel string
	}{
		{ref: "fallback-model", wantModel: "fallback-model"},
		{ref: "other-model", wantModel: "other-model"},
		{ref: ""},
	}
	for _, step := range steps {
		live = step.ref
		req := buildRunRequest(r, compactionFallbackSetup(t, r), tool.NewRegistry(), nil, runHooks{})
		if step.ref == "" {
			if req.CompactionFallback.Resolve != nil {
				t.Fatal("CompactionFallback.Resolve set after live ref cleared, want disabled")
			}
			continue
		}
		if req.CompactionFallback.Ref != step.ref {
			t.Fatalf("CompactionFallback.Ref = %q, want %q", req.CompactionFallback.Ref, step.ref)
		}
		target, err := req.CompactionFallback.Resolve(context.Background())
		if err != nil {
			t.Fatalf("Resolve() for %q error = %v", step.ref, err)
		}
		if got := target.ResolvedModel.BackendModelID; got != step.wantModel {
			t.Fatalf("resolved backend model = %q, want %q", got, step.wantModel)
		}
	}
}

func TestBuildRunRequestCompactionFallbackMemoScope(t *testing.T) {
	rec := &fallbackBuildRecorder{}
	r := compactionFallbackRunner(compactionFallbackRuntimeConfig("fallback-model"), rec.factory, nil)
	setup := compactionFallbackSetup(t, r)

	resolve := func(r cliRunner) provider.Provider {
		t.Helper()
		req := buildRunRequest(r, setup, tool.NewRegistry(), nil, runHooks{})
		target, err := req.CompactionFallback.Resolve(context.Background())
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		return target.Provider
	}

	first := resolve(r)
	copied := r // a value copy must share the memo owner
	if got := resolve(copied); got != first {
		t.Fatal("value copy built a new provider, want the memoized provider")
	}
	if len(rec.models) != 1 {
		t.Fatalf("provider builds = %d, want 1", len(rec.models))
	}

	r.promptCacheKeyFn = func() string { return "cache-key-2" }
	if got := resolve(r); got == first {
		t.Fatal("new cache scope reused the provider, want a fresh build")
	}
	if len(rec.models) != 2 {
		t.Fatalf("provider builds = %d, want 2 after cache scope change", len(rec.models))
	}
}

func TestWireInteractiveRunnerInstallsCompactionFallbackMemo(t *testing.T) {
	newRunner := func(t *testing.T) cliRunner {
		t.Helper()
		rt := cliRuntime{events: output.NoopSink{}, workDir: t.TempDir(), homeDir: t.TempDir()}
		sess, err := buildInteractiveSession(rt)
		if err != nil {
			t.Fatalf("buildInteractiveSession() error = %v", err)
		}
		wireInteractiveRunner(rt, sess)
		return wiredSessionRunner(t, sess).runner
	}

	first := newRunner(t)
	if first.compactionTargets == nil {
		t.Fatal("interactive runner compactionTargets = nil, want session-owned memo")
	}
	second := newRunner(t)
	if second.compactionTargets == nil {
		t.Fatal("second interactive runner compactionTargets = nil, want session-owned memo")
	}
	if first.compactionTargets == second.compactionTargets {
		t.Fatal("two interactive sessions share one memo, want per-session memos")
	}
}

// stubPhaseRuntime makes newPhaseRunner build its runtime from cfg. The base
// provider factory counts its builds in builds.
func stubPhaseRuntime(t *testing.T, cfg config.Config, builds *int) {
	t.Helper()
	oldBuildPhaseRuntime := buildPhaseRuntime
	t.Cleanup(func() { buildPhaseRuntime = oldBuildPhaseRuntime })
	buildPhaseRuntime = func(context.Context, *cobra.Command, *cliFlags, string, string, string) (cliRuntime, error) {
		return cliRuntime{
			cfg: cfg,
			providerFactory: func(provider.ResolvedModel, string) (provider.Provider, error) {
				*builds++
				return &fakeProvider{}, nil
			},
		}, nil
	}
}

func TestNewPhaseRunnerInstallsCompactionFallbackMemo(t *testing.T) {
	builds := 0
	stubPhaseRuntime(t, compactionFallbackRuntimeConfig("fallback-model"), &builds)

	runner, err := newPhaseRunner(context.Background(), nil, &cliFlags{}, phaseRunnerParams{})
	if err != nil {
		t.Fatalf("newPhaseRunner() error = %v", err)
	}
	if phase := runner.(phaseRunner); phase.runner.compactionTargets == nil {
		t.Fatal("oneshot phase runner compactionTargets = nil, want phase-owned memo")
	}
}

func TestCompactionFallbackOneshotRejectsClaudeSubscription(t *testing.T) {
	builds := 0
	stubPhaseRuntime(t, compactionFallbackRuntimeConfig("claude-model"), &builds)

	runner, err := newPhaseRunner(context.Background(), nil, &cliFlags{}, phaseRunnerParams{})
	if err != nil {
		t.Fatalf("newPhaseRunner() error = %v", err)
	}
	phase := runner.(phaseRunner)
	req := buildRunRequest(phase.runner, compactionFallbackSetup(t, phase.runner), tool.NewRegistry(), nil, runHooks{})
	if req.CompactionFallback.Resolve == nil {
		t.Fatal("CompactionFallback.Resolve = nil, want enabled fallback")
	}

	_, err = req.CompactionFallback.Resolve(context.Background())
	if !errors.Is(err, oneshot.ErrClaudeSubscription) {
		t.Fatalf("Resolve() error = %v, want %v", err, oneshot.ErrClaudeSubscription)
	}
	if builds != 0 {
		t.Fatalf("base provider factory builds = %d, want 0 for rejected claude_subscription fallback", builds)
	}
}
