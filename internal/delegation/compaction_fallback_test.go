package delegation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

const compactionFallbackTestRef = "fallback"

// compactionFallbackTestProvider is a provider.Provider tagged with the build
// number that created it, so tests can tell providers apart.
type compactionFallbackTestProvider struct {
	build int
}

func (*compactionFallbackTestProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (*compactionFallbackTestProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, nil
}

func (*compactionFallbackTestProvider) SupportsUsageStats() bool {
	return false
}

func compactionFallbackTestConfig(ref string) *config.Config {
	return &config.Config{
		Providers: map[string]config.ProviderConfig{"main": {Type: config.ProviderTypeAnthropic}},
		Models: config.ModelsConfig{
			Effective: config.EffectiveModelAssignments{CompactionFallback: ref},
		},
	}
}

func compactionFallbackTestMain() provider.ResolvedModel {
	return provider.ResolvedModel{
		ProviderAlias:  "main",
		BackendModelID: "main-model",
		EffectiveLimits: provider.EffectiveLimits{
			ContextWindow:             200000,
			MaxOutputTokens:           4096,
			CompactionThresholdTokens: 140000,
		},
	}
}

func compactionFallbackTestModel(providerAlias, backendID string) provider.ResolvedModel {
	return provider.ResolvedModel{
		Alias:          backendID,
		ProviderAlias:  providerAlias,
		BackendModelID: backendID,
		EffectiveLimits: provider.EffectiveLimits{
			ContextWindow:             32768,
			MaxOutputTokens:           4096,
			CompactionThresholdTokens: 20000,
			EstimatorPadTokens:        327,
			NormalSummaryMaxTokens:    4096,
			EmergencySummaryMaxTokens: 3072,
			CompactionThreshold:       0.70,
		},
	}
}

// compactionFallbackTestParams returns enabled params for ref with a fresh
// Targets memo.
func compactionFallbackTestParams(ref string, resolve func(string) (provider.ResolvedModel, error), factory func(provider.ResolvedModel, string) (provider.Provider, error)) CompactionFallbackParams {
	return CompactionFallbackParams{
		Config:          compactionFallbackTestConfig(ref),
		Main:            compactionFallbackTestMain(),
		ResolveModel:    resolve,
		ProviderFactory: factory,
		SessionID:       "session-1",
		Targets:         NewCompactionFallbackTargets(),
	}
}

// compactionFallbackTestResolver returns a ResolveModel that accepts only ref,
// returns model, and counts calls.
func compactionFallbackTestResolver(ref string, model provider.ResolvedModel, calls *atomic.Int32) func(string) (provider.ResolvedModel, error) {
	return func(got string) (provider.ResolvedModel, error) {
		calls.Add(1)
		if got != ref {
			return provider.ResolvedModel{}, fmt.Errorf("resolve ref %q, want %q", got, ref)
		}
		return model, nil
	}
}

// compactionFallbackTestFactory returns a ProviderFactory that counts builds and
// returns a provider tagged with its build number.
func compactionFallbackTestFactory(builds *atomic.Int32) func(provider.ResolvedModel, string) (provider.Provider, error) {
	return func(provider.ResolvedModel, string) (provider.Provider, error) {
		return &compactionFallbackTestProvider{build: int(builds.Add(1))}, nil
	}
}

func TestNewCompactionFallbackDisabled(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*CompactionFallbackParams)
	}{
		{name: "nil config", modify: func(p *CompactionFallbackParams) { p.Config = nil }},
		{name: "nil targets", modify: func(p *CompactionFallbackParams) { p.Targets = nil }},
		{name: "nil resolver", modify: func(p *CompactionFallbackParams) { p.ResolveModel = nil }},
		{name: "nil factory", modify: func(p *CompactionFallbackParams) { p.ProviderFactory = nil }},
		{name: "empty ref", modify: func(p *CompactionFallbackParams) { p.Config.Models.Effective.CompactionFallback = "" }},
		{name: "blank ref", modify: func(p *CompactionFallbackParams) { p.Config.Models.Effective.CompactionFallback = "  " }},
		{name: "explicit zero delay", modify: func(p *CompactionFallbackParams) {
			p.Config.Providers["main"] = config.ProviderConfig{Type: config.ProviderTypeAnthropic, CompactionFallbackAfter: config.MustDuration("0s")}
		}},
		{name: "unknown main provider alias", modify: func(p *CompactionFallbackParams) { p.Main.ProviderAlias = "missing" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resolves, builds atomic.Int32
			model := compactionFallbackTestModel("fb", "fb-model")
			p := compactionFallbackTestParams(compactionFallbackTestRef, compactionFallbackTestResolver(compactionFallbackTestRef, model, &resolves), compactionFallbackTestFactory(&builds))
			tt.modify(&p)

			got := NewCompactionFallback(p)
			if got.After != 0 || got.Ref != "" || got.Resolve != nil {
				t.Errorf("NewCompactionFallback() = {After: %v, Ref: %q, Resolve nil: %v}, want zero value", got.After, got.Ref, got.Resolve == nil)
			}
			if resolves.Load() != 0 || builds.Load() != 0 {
				t.Errorf("disabled fallback resolved %d models and built %d providers, want none", resolves.Load(), builds.Load())
			}
		})
	}
}

func TestNewCompactionFallbackEnabledWithZeroTokenThreshold(t *testing.T) {
	var resolves, builds atomic.Int32
	model := compactionFallbackTestModel("fb", "fb-model")
	p := compactionFallbackTestParams(compactionFallbackTestRef, compactionFallbackTestResolver(compactionFallbackTestRef, model, &resolves), compactionFallbackTestFactory(&builds))
	p.Main.EffectiveLimits.CompactionThresholdTokens = 0

	got := NewCompactionFallback(p)
	if got.Resolve == nil || got.Ref != compactionFallbackTestRef || got.After != 5*time.Minute {
		t.Fatalf("NewCompactionFallback() = {After: %v, Ref: %q, Resolve nil: %v}, want enabled with 5m delay", got.After, got.Ref, got.Resolve == nil)
	}
	if _, err := got.Resolve(context.Background()); err != nil {
		t.Errorf("Resolve: %v", err)
	}
}

func TestNewCompactionFallbackFields(t *testing.T) {
	model := compactionFallbackTestModel("fb", "fb-model")
	tests := []struct {
		name      string
		ref       string
		after     config.Duration
		wantRef   string
		wantAfter time.Duration
	}{
		{name: "unset delay uses provider default", ref: "fallback", wantRef: "fallback", wantAfter: 5 * time.Minute},
		{name: "explicit delay", ref: "fallback", after: config.MustDuration("2m"), wantRef: "fallback", wantAfter: 2 * time.Minute},
		{name: "ref is trimmed", ref: " fallback ", wantRef: "fallback", wantAfter: 5 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resolves, builds atomic.Int32
			p := compactionFallbackTestParams(tt.ref, compactionFallbackTestResolver(tt.wantRef, model, &resolves), compactionFallbackTestFactory(&builds))
			p.Config.Providers["main"] = config.ProviderConfig{Type: config.ProviderTypeAnthropic, CompactionFallbackAfter: tt.after}

			got := NewCompactionFallback(p)
			if got.Resolve == nil {
				t.Fatal("Resolve is nil, want lazy resolver")
			}
			if got.Ref != tt.wantRef {
				t.Errorf("Ref = %q, want %q", got.Ref, tt.wantRef)
			}
			if got.After != tt.wantAfter {
				t.Errorf("After = %v, want %v", got.After, tt.wantAfter)
			}
		})
	}
}

func TestCompactionFallbackResolveIsLazy(t *testing.T) {
	var resolves, builds atomic.Int32
	model := compactionFallbackTestModel("fb", "fb-model")
	p := compactionFallbackTestParams(compactionFallbackTestRef, compactionFallbackTestResolver(compactionFallbackTestRef, model, &resolves), compactionFallbackTestFactory(&builds))
	fb := NewCompactionFallback(p)
	if resolves.Load() != 0 || builds.Load() != 0 {
		t.Fatalf("NewCompactionFallback resolved %d models and built %d providers, want none", resolves.Load(), builds.Load())
	}

	if _, err := fb.Resolve(context.Background()); err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	if resolves.Load() != 1 || builds.Load() != 1 {
		t.Errorf("after first Resolve: resolves = %d, builds = %d, want 1, 1", resolves.Load(), builds.Load())
	}

	if _, err := fb.Resolve(context.Background()); err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if resolves.Load() != 2 || builds.Load() != 1 {
		t.Errorf("after second Resolve: resolves = %d, builds = %d, want 2, 1", resolves.Load(), builds.Load())
	}
}

func TestCompactionFallbackResolveBuildsTarget(t *testing.T) {
	var resolves, builds atomic.Int32
	model := compactionFallbackTestModel("fb", "fb-model")
	var gotSession string
	factory := func(_ provider.ResolvedModel, session string) (provider.Provider, error) {
		gotSession = session
		return &compactionFallbackTestProvider{build: int(builds.Add(1))}, nil
	}
	p := compactionFallbackTestParams(compactionFallbackTestRef, compactionFallbackTestResolver(compactionFallbackTestRef, model, &resolves), factory)

	target, err := NewCompactionFallback(p).Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if gotSession != p.SessionID {
		t.Errorf("factory session = %q, want %q", gotSession, p.SessionID)
	}
	prov, ok := target.Provider.(*compactionFallbackTestProvider)
	if !ok || prov.build != 1 {
		t.Errorf("Provider = %#v, want provider from build 1", target.Provider)
	}
	if !reflect.DeepEqual(target.ResolvedModel, model) {
		t.Errorf("ResolvedModel = %#v, want %#v", target.ResolvedModel, model)
	}
	if want := prompt.ModelBudgetFromEffectiveLimits(model.EffectiveLimits); !reflect.DeepEqual(target.ModelBudget, want) {
		t.Errorf("ModelBudget = %#v, want %#v", target.ModelBudget, want)
	}
}

func TestCompactionFallbackResolveContextCanceled(t *testing.T) {
	var resolves, builds atomic.Int32
	model := compactionFallbackTestModel("fb", "fb-model")
	p := compactionFallbackTestParams(compactionFallbackTestRef, compactionFallbackTestResolver(compactionFallbackTestRef, model, &resolves), compactionFallbackTestFactory(&builds))
	fb := NewCompactionFallback(p)
	if _, err := fb.Resolve(context.Background()); err != nil {
		t.Fatalf("Resolve with live context: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fb.Resolve(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Resolve with canceled context error = %v, want context.Canceled", err)
	}
	if resolves.Load() != 1 || builds.Load() != 1 {
		t.Errorf("canceled Resolve: resolves = %d, builds = %d, want 1, 1", resolves.Load(), builds.Load())
	}
}

func TestCompactionFallbackNilProviderNotCached(t *testing.T) {
	var resolves, builds atomic.Int32
	model := compactionFallbackTestModel("fb", "fb-model")
	p := compactionFallbackTestParams(compactionFallbackTestRef, compactionFallbackTestResolver(compactionFallbackTestRef, model, &resolves), func(provider.ResolvedModel, string) (provider.Provider, error) {
		n := builds.Add(1)
		if n == 1 {
			return nil, nil
		}
		return &compactionFallbackTestProvider{build: int(n)}, nil
	})
	fb := NewCompactionFallback(p)

	if _, err := fb.Resolve(context.Background()); err == nil {
		t.Fatal("first Resolve with nil provider: error = nil, want error")
	}
	target, err := fb.Resolve(context.Background())
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if target.Provider == nil {
		t.Fatal("second Resolve provider = nil, want built provider")
	}
	if _, err := fb.Resolve(context.Background()); err != nil {
		t.Fatalf("third Resolve: %v", err)
	}
	if got := builds.Load(); got != 2 {
		t.Errorf("builds = %d, want 2 (nil provider not cached, success cached)", got)
	}
}

// TestCompactionFallbackKeyDoesNotCollide checks identity pairs whose "/"-joined
// forms are equal but whose fields differ. Each pair must build separate
// targets. Each pair joins to the same string under the old concatenated key.
func TestCompactionFallbackKeyDoesNotCollide(t *testing.T) {
	tests := []struct {
		name          string
		first, second provider.ResolvedModel
	}{
		{
			name:   "joins to a/model b/c",
			first:  compactionFallbackTestModel("a/model b", "c"),
			second: compactionFallbackTestModel("a", "model b/c"),
		},
		{
			name:   "joins to a/b/model c",
			first:  compactionFallbackTestModel("a/b", "model c"),
			second: compactionFallbackTestModel("a", "b/model c"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets := NewCompactionFallbackTargets()
			var builds atomic.Int32
			factory := compactionFallbackTestFactory(&builds)
			var providers []provider.Provider
			for i, model := range []provider.ResolvedModel{tt.first, tt.second} {
				var resolves atomic.Int32
				p := compactionFallbackTestParams(compactionFallbackTestRef, compactionFallbackTestResolver(compactionFallbackTestRef, model, &resolves), factory)
				p.Targets = targets
				target, err := NewCompactionFallback(p).Resolve(context.Background())
				if err != nil {
					t.Fatalf("identity %d Resolve: %v", i, err)
				}
				providers = append(providers, target.Provider)
			}
			if providers[0] == providers[1] {
				t.Error("distinct identities share a provider, want separate targets")
			}
			if got := builds.Load(); got != 2 {
				t.Errorf("builds = %d, want 2", got)
			}
		})
	}
}

// TestCompactionFallbackColdCanceledResolve checks that a canceled Resolve on a
// cold memo returns before ResolveModel or the factory runs.
func TestCompactionFallbackColdCanceledResolve(t *testing.T) {
	var resolves, builds atomic.Int32
	model := compactionFallbackTestModel("fb", "fb-model")
	p := compactionFallbackTestParams(compactionFallbackTestRef, compactionFallbackTestResolver(compactionFallbackTestRef, model, &resolves), compactionFallbackTestFactory(&builds))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewCompactionFallback(p).Resolve(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Resolve error = %v, want context.Canceled", err)
	}
	if resolves.Load() != 0 || builds.Load() != 0 {
		t.Errorf("cold canceled Resolve: resolves = %d, builds = %d, want 0, 0", resolves.Load(), builds.Load())
	}
}

func TestCompactionFallbackRetriesErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name        string
		failResolve bool
		failBuild   bool
		wantBuilds  int32
	}{
		{name: "resolve error is not cached", failResolve: true, wantBuilds: 1},
		{name: "factory error is not cached", failBuild: true, wantBuilds: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resolves, builds atomic.Int32
			model := compactionFallbackTestModel("fb", "fb-model")
			p := compactionFallbackTestParams(compactionFallbackTestRef,
				func(string) (provider.ResolvedModel, error) {
					if n := resolves.Add(1); tt.failResolve && n == 1 {
						return provider.ResolvedModel{}, boom
					}
					return model, nil
				},
				func(provider.ResolvedModel, string) (provider.Provider, error) {
					n := builds.Add(1)
					if tt.failBuild && n == 1 {
						return nil, boom
					}
					return &compactionFallbackTestProvider{build: int(n)}, nil
				},
			)
			fb := NewCompactionFallback(p)

			if _, err := fb.Resolve(context.Background()); !errors.Is(err, boom) {
				t.Fatalf("first Resolve error = %v, want %v", err, boom)
			}
			if _, err := fb.Resolve(context.Background()); err != nil {
				t.Fatalf("second Resolve: %v", err)
			}
			if _, err := fb.Resolve(context.Background()); err != nil {
				t.Fatalf("third Resolve: %v", err)
			}
			if got := builds.Load(); got != tt.wantBuilds {
				t.Errorf("builds = %d, want %d (success cached after retry)", got, tt.wantBuilds)
			}
		})
	}
}

type compactionFallbackStep struct {
	ref   string
	scope string
	model provider.ResolvedModel
}

func TestCompactionFallbackMemo(t *testing.T) {
	a := compactionFallbackTestModel("p1", "a")
	b := compactionFallbackTestModel("p1", "b")
	otherProvider := compactionFallbackTestModel("p2", "a")
	tests := []struct {
		name       string
		steps      []compactionFallbackStep
		wantBuilds int32
		// wantShared[i] reports whether step i+1 returns the same provider as step 0.
		wantShared []bool
	}{
		{
			name:       "same ref and scope share",
			steps:      []compactionFallbackStep{{ref: "fb", model: a}, {ref: "fb", model: a}},
			wantBuilds: 1,
			wantShared: []bool{true},
		},
		{
			name:       "different refs resolving to the same identity share",
			steps:      []compactionFallbackStep{{ref: "fb1", scope: "s", model: a}, {ref: "fb2", scope: "s", model: a}},
			wantBuilds: 1,
			wantShared: []bool{true},
		},
		{
			name:       "empty scope differs from named scope",
			steps:      []compactionFallbackStep{{ref: "fb", model: a}, {ref: "fb", scope: "s", model: a}},
			wantBuilds: 2,
			wantShared: []bool{false},
		},
		{
			name:       "different cache scopes do not share",
			steps:      []compactionFallbackStep{{ref: "fb", scope: "s1", model: a}, {ref: "fb", scope: "s2", model: a}},
			wantBuilds: 2,
			wantShared: []bool{false},
		},
		{
			name:       "different backend model IDs do not share",
			steps:      []compactionFallbackStep{{ref: "fb", model: a}, {ref: "fb", model: b}},
			wantBuilds: 2,
			wantShared: []bool{false},
		},
		{
			name:       "different provider aliases do not share",
			steps:      []compactionFallbackStep{{ref: "fb", model: a}, {ref: "fb", model: otherProvider}},
			wantBuilds: 2,
			wantShared: []bool{false},
		},
		{
			name: "remap builds the new identity and reuses the earlier one",
			steps: []compactionFallbackStep{
				{ref: "fb", model: a},
				{ref: "fb", model: a},
				{ref: "fb", model: b},
				{ref: "fb", model: a},
			},
			wantBuilds: 2,
			wantShared: []bool{true, false, true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var builds atomic.Int32
			factory := compactionFallbackTestFactory(&builds)
			targets := NewCompactionFallbackTargets()
			var first provider.Provider
			for i, step := range tt.steps {
				var resolves atomic.Int32
				p := compactionFallbackTestParams(step.ref, compactionFallbackTestResolver(step.ref, step.model, &resolves), factory)
				p.Targets = targets
				p.CacheScope = step.scope

				target, err := NewCompactionFallback(p).Resolve(context.Background())
				if err != nil {
					t.Fatalf("step %d Resolve: %v", i, err)
				}
				if i == 0 {
					first = target.Provider
					continue
				}
				if got := target.Provider == first; got != tt.wantShared[i-1] {
					t.Errorf("step %d shares step 0 provider = %v, want %v", i, got, tt.wantShared[i-1])
				}
			}
			if got := builds.Load(); got != tt.wantBuilds {
				t.Errorf("builds = %d, want %d", got, tt.wantBuilds)
			}
		})
	}
}

func TestCompactionFallbackConcurrentSharedMemo(t *testing.T) {
	const workers = 32
	var resolves, builds atomic.Int32
	model := compactionFallbackTestModel("fb", "fb-model")
	p := compactionFallbackTestParams(compactionFallbackTestRef, compactionFallbackTestResolver(compactionFallbackTestRef, model, &resolves), compactionFallbackTestFactory(&builds))
	fb := NewCompactionFallback(p)

	start := make(chan struct{})
	providers := make([]provider.Provider, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			target, err := fb.Resolve(context.Background())
			providers[i] = target.Provider
			errs[i] = err
		}()
	}
	close(start)
	wg.Wait()

	for i := range workers {
		if errs[i] != nil {
			t.Fatalf("worker %d Resolve: %v", i, errs[i])
		}
		if providers[i] == nil || providers[i] != providers[0] {
			t.Fatalf("worker %d provider = %#v, want shared provider %#v", i, providers[i], providers[0])
		}
	}
	if got := builds.Load(); got != 1 {
		t.Errorf("builds = %d, want 1", got)
	}
	if got := resolves.Load(); got != workers {
		t.Errorf("resolves = %d, want %d (ResolveModel runs on every call)", got, workers)
	}
}
