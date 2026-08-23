package interactive

import "github.com/luispabon/steiner/internal/config"

func currentModelConfig(cfg config.Config) config.ModelConfig {
	model, _ := config.ResolveModelConfig(&cfg, cfg.Models.Default)
	return model
}
