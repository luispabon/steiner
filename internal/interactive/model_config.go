package interactive

import "github.com/luispabon/steiner/internal/config"

// SelectedModelConfig returns the model config for the active orchestrator
// model, falling back to the default model when none is active.
func SelectedModelConfig(cfg config.Config) config.ModelConfig {
	alias := cfg.Models.Effective.ActiveOrchestratorModel
	if alias == "" {
		alias = cfg.Models.Effective.DefaultModel
	}
	model, _ := config.ResolveModelConfig(&cfg, alias)
	return model
}
