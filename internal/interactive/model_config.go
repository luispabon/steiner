package interactive

import "github.com/luispabon/steiner/internal/config"

func currentModelConfig(cfg config.Config) config.ModelConfig {
	if model, ok := cfg.Models.Definitions[cfg.Models.Default]; ok {
		return model
	}
	providerAlias, modelID, err := config.ParseModelReference(&cfg, cfg.Models.Default)
	if err != nil {
		return config.ModelConfig{}
	}
	model := config.NewModelConfigBase()
	model.Provider = providerAlias
	model.ID = modelID
	return model
}
