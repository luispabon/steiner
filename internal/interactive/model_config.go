package interactive

import "github.com/luispabon/steiner/internal/config"

func currentModelConfig(cfg config.Config) config.ModelConfig {
	alias := cfg.Models.Effective.ActiveOrchestratorModel
	if alias == "" {
		alias = cfg.Models.Effective.DefaultModel
	}
	model, _ := config.ResolveModelConfig(&cfg, alias)
	return model
}
