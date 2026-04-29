package main

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

func selectedModelConfig(cfg config.Config) (config.ModelConfig, error) {
	return cfg.Model, nil
}

func selectedModelConfigByAlias(cfg config.Config, alias string) (config.ModelConfig, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return config.ModelConfig{}, fmt.Errorf("model is required")
	}
	model, ok := cfg.Models[alias]
	if !ok {
		return config.ModelConfig{}, fmt.Errorf("model %q is not defined", alias)
	}
	return model, nil
}

func switchModelConfigByAlias(cfg *config.Config, alias string) (config.ModelConfig, error) {
	if cfg == nil {
		return config.ModelConfig{}, fmt.Errorf("config is required")
	}
	model, err := selectedModelConfigByAlias(*cfg, alias)
	if err != nil {
		return config.ModelConfig{}, err
	}
	cfg.Model = model
	return model, nil
}

func modelAliasNames(cfg config.Config) []string {
	if len(cfg.Models) == 0 {
		return nil
	}
	names := make([]string, 0, len(cfg.Models))
	for k := range cfg.Models {
		names = append(names, k)
	}
	return names
}

func modelContextSizes(cfg config.Config) map[string]int {
	if len(cfg.Models) == 0 {
		return nil
	}
	sizes := make(map[string]int, len(cfg.Models))
	for name, model := range cfg.Models {
		if model.ContextSize > 0 {
			sizes[name] = model.ContextSize
		}
	}
	return sizes
}
