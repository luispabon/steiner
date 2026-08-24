package config

import (
	"fmt"
	"strings"
)

// ParseModelReference resolves a model alias or provider/model-id reference.
func ParseModelReference(cfg *Config, ref string) (providerAlias string, modelID string, err error) {
	if cfg == nil || strings.TrimSpace(ref) == "" {
		return "", "", fmt.Errorf("invalid model reference %q", ref)
	}
	if model, ok := cfg.Models.Definitions[ref]; ok {
		if model.Provider == "" || strings.TrimSpace(model.ID) == "" {
			return "", "", fmt.Errorf("model reference %q has no concrete provider and model id", ref)
		}
		return model.Provider, model.ID, nil
	}

	best := ""
	for provider := range cfg.Providers {
		if ref == provider || strings.HasPrefix(ref, provider+"/") && len(provider) > len(best) {
			best = provider
		}
	}
	if best == "" {
		return "", "", fmt.Errorf("model reference %q is not a defined model alias or provider/model-id reference", ref)
	}
	modelID = strings.TrimPrefix(ref[len(best):], "/")
	if strings.TrimSpace(modelID) == "" {
		return "", "", fmt.Errorf("model reference %q must include a model id", ref)
	}
	return best, modelID, nil
}

// ResolveModelConfig resolves a configured model definition or provider/model-id reference.
func ResolveModelConfig(cfg *Config, ref string) (ModelConfig, bool) {
	if cfg != nil {
		if model, ok := cfg.Models.Definitions[ref]; ok {
			return model, true
		}
	}
	providerAlias, modelID, err := ParseModelReference(cfg, ref)
	if err != nil {
		return ModelConfig{}, false
	}
	model := NewModelConfigBase()
	model.Provider = providerAlias
	model.ID = modelID
	// Leave limits unset (rather than the base 32768/8192 defaults) so
	// resolveLimitsFromDiscovery treats them as undiscovered and runs
	// provider/models.dev discovery instead of short-circuiting on
	// limitsFullyConfigured.
	model.Advanced.Limits = AdvancedLimitsConfig{}
	return model, false
}

// IsValidModelReference reports whether ref is a valid model alias or provider/model-id reference.
func IsValidModelReference(cfg *Config, ref string) bool {
	_, _, err := ParseModelReference(cfg, ref)
	return err == nil
}
