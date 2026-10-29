package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// CompactionFallbackTargets memoizes compaction fallback targets so repeated
// compactions reuse one provider per identity. A target is keyed by the resolved
// provider alias, backend model ID and cache scope. Failed builds are not
// memoized, and targets are never closed here. Create it with
// NewCompactionFallbackTargets.
type CompactionFallbackTargets struct {
	mu      sync.Mutex
	targets map[compactionTargetKey]agent.CompactionTarget
}

// compactionTargetKey identifies a memoized target. Each identity field is kept
// separate and compared as a whole, so a "/" inside an alias or model ID cannot
// make two different identities collide.
type compactionTargetKey struct {
	providerAlias  string
	backendModelID string
	cacheScope     string
}

// NewCompactionFallbackTargets returns an empty CompactionFallbackTargets.
func NewCompactionFallbackTargets() *CompactionFallbackTargets {
	return &CompactionFallbackTargets{targets: make(map[compactionTargetKey]agent.CompactionTarget)}
}

// CompactionFallbackParams configures NewCompactionFallback.
type CompactionFallbackParams struct {
	// Config supplies the effective fallback ref and the cold-cache delay.
	Config *config.Config
	// Main is the main model. Its provider alias selects the cold-cache delay.
	Main provider.ResolvedModel
	// ResolveModel resolves the fallback ref. It is called on every resolve.
	ResolveModel func(string) (provider.ResolvedModel, error)
	// ProviderFactory builds the provider for a resolved model. It is called once
	// per memoized identity and cache scope.
	ProviderFactory func(provider.ResolvedModel, string) (provider.Provider, error)
	// SessionID is passed to ProviderFactory as transport affinity.
	SessionID string
	// Targets memoizes built targets across resolves. It must be non-nil.
	Targets *CompactionFallbackTargets
	// CacheScope separates targets that must not share a provider. An empty
	// scope is valid.
	CacheScope string
}

// NewCompactionFallback returns the lazy compaction fallback for p. It returns
// the zero agent.CompactionFallback, which disables the fallback, when Config,
// Targets, ResolveModel or ProviderFactory is nil, when the trimmed fallback ref
// is empty, or when Config.CompactionFallbackAfter for p.Main's provider alias is
// not positive. That covers an explicit zero delay and an unknown alias. The
// main model's token threshold does not affect enablement. Otherwise the
// returned Resolve resolves the ref on every call and reuses or builds the
// target for the resolved model. Nothing is resolved or built before Resolve
// runs.
func NewCompactionFallback(p CompactionFallbackParams) agent.CompactionFallback {
	if p.Config == nil || p.Targets == nil || p.ResolveModel == nil || p.ProviderFactory == nil {
		return agent.CompactionFallback{}
	}
	ref := strings.TrimSpace(p.Config.Models.Effective.CompactionFallback)
	after := p.Config.CompactionFallbackAfter(p.Main.ProviderAlias)
	if ref == "" || after <= 0 {
		return agent.CompactionFallback{}
	}
	return agent.CompactionFallback{
		After: after,
		Ref:   ref,
		Resolve: func(ctx context.Context) (agent.CompactionTarget, error) {
			if err := ctx.Err(); err != nil {
				return agent.CompactionTarget{}, err
			}
			model, err := p.ResolveModel(ref)
			if err != nil {
				return agent.CompactionTarget{}, fmt.Errorf("resolve compaction fallback model %q: %w", ref, err)
			}
			return p.Targets.target(model, p.SessionID, p.CacheScope, p.ProviderFactory)
		},
	}
}

// target returns the memoized target for model's identity and cache scope. On a
// miss it builds the provider with factory while holding the mutex, so
// concurrent callers build each identity once. A failed build, including a
// factory that returns a nil provider without error, is returned and not
// memoized.
func (t *CompactionFallbackTargets) target(model provider.ResolvedModel, sessionID, cacheScope string, factory func(provider.ResolvedModel, string) (provider.Provider, error)) (agent.CompactionTarget, error) {
	key := compactionTargetKey{providerAlias: model.ProviderAlias, backendModelID: model.BackendModelID, cacheScope: cacheScope}
	t.mu.Lock()
	defer t.mu.Unlock()
	if target, ok := t.targets[key]; ok {
		return target, nil
	}
	prov, err := factory(model, sessionID)
	if err != nil {
		return agent.CompactionTarget{}, fmt.Errorf("build compaction fallback provider for %q: %w", model.Alias, err)
	}
	if prov == nil {
		return agent.CompactionTarget{}, errors.New("build compaction fallback provider: factory returned nil provider")
	}
	target := agent.CompactionTarget{
		Provider:      prov,
		ResolvedModel: model,
		ModelBudget:   prompt.ModelBudgetFromEffectiveLimits(model.EffectiveLimits),
	}
	if t.targets == nil {
		t.targets = make(map[compactionTargetKey]agent.CompactionTarget)
	}
	t.targets[key] = target
	return target, nil
}
