package oneshot

import (
	"errors"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

// ErrClaudeSubscription refuses a oneshot run that can route to the
// claude_subscription provider.
var ErrClaudeSubscription = errors.New("the claude_subscription provider cannot be used for oneshot runs; choose another provider for oneshot")

// RequireSubAgents returns an error if sub-agents are disabled in cfg, since
// oneshot runs depend on sub-agent delegation.
func RequireSubAgents(cfg config.Config) error {
	if !cfg.SubAgent.Enabled {
		return errors.New("oneshot unavailable: sub-agents are disabled in config")
	}
	return nil
}

// RequireModels returns an error wrapping config.ErrNoModelConfigured when a
// oneshot phase or the advisor has no model reference in cfg. A phase uses its
// own selection or the profile default; the advisor uses the advisor selection
// or the profile default. It never falls back to the active orchestrator model.
func RequireModels(cfg config.Config) error {
	for _, phase := range phaseOrder() {
		if err := config.RequireModelReference(cfg, phaseModelAlias(cfg, phase)); err != nil {
			return fmt.Errorf("oneshot %s phase model: %w", phase, err)
		}
	}
	advisor := strings.TrimSpace(cfg.Models.Effective.Advisor)
	if advisor == "" {
		advisor = strings.TrimSpace(cfg.Models.Effective.DefaultModel)
	}
	if err := config.RequireModelReference(cfg, advisor); err != nil {
		return fmt.Errorf("oneshot advisor model: %w", err)
	}
	return nil
}

// RequireProviders refuses a oneshot run when a model alias the run can
// route to resolves to the claude_subscription provider. It reads configuration
// only, so it runs before any run identity, lock, manifest or worktree exists.
// The providerFactory wrapper in cmd/steiner stays as the backstop.
func RequireProviders(cfg config.Config) error {
	for _, alias := range modelAliases(cfg) {
		modelCfg, ok := config.ResolveModelConfig(&cfg, strings.TrimSpace(alias))
		if !ok {
			continue
		}
		if cfg.Providers[modelCfg.Provider].Type == config.ProviderTypeClaudeSubscription {
			return ErrClaudeSubscription
		}
	}
	return nil
}

// modelAliases lists the configured aliases a oneshot run can use: the
// advisor, the default model that phases and sub-agents fall back to, each
// phase model, and each sub-agent model.
func modelAliases(cfg config.Config) []string {
	effective := cfg.Models.Effective
	aliases := []string{effective.DefaultModel, effective.Advisor}
	for _, alias := range effective.OneShot {
		aliases = append(aliases, alias)
	}
	for _, alias := range effective.SubAgents {
		aliases = append(aliases, alias)
	}
	return aliases
}
