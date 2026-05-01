package main

import (
	"github.com/luispabon/steiner/internal/config"
)

func selectedModelConfig(cfg config.Config) (config.ModelConfig, error) {
	return cfg.Model, nil
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

func modelBaseURLs(cfg config.Config) map[string]string {
	if len(cfg.Models) == 0 {
		return nil
	}
	urls := make(map[string]string, len(cfg.Models))
	for name, model := range cfg.Models {
		urls[name] = model.BaseURL
	}
	return urls
}
