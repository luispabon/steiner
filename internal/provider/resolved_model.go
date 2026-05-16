package provider

import (
	"fmt"

	"github.com/luispabon/steiner/internal/config"
)

// EffectiveLimits holds the runtime-resolved token limits for a model.
type EffectiveLimits struct {
	ContextWindow       int
	MaxInputTokens      int
	MaxOutputTokens     int
	OutputReserveTokens int
	SafetyMarginTokens  int
	SummaryMaxTokens    int
	CompactionThreshold float64
}

// ResolvedModel is the runtime object combining provider and model config
// with resolved metadata.
type ResolvedModel struct {
	Alias                     string
	ProviderAlias             string
	ProviderConfig            config.ProviderConfig
	BackendModelID            string
	EffectiveLimits           EffectiveLimits
	Params                    map[string]any
	ExtraParams               map[string]any
	ThinkingEnabled           bool
	ThinkingDisableMarker     string
	ThinkingScaffoldInference bool
	ThinkingParams            map[string]any
	Prompts                   config.ModelPrompts
	Retry                     config.RetryConfig
	MetadataSource            string
	Confidence                string
	Warnings                  []string
}

// Resolve builds a ResolvedModel from cfg for the given model alias.
// It returns an error if the alias is not found or the provider is not found.
func Resolve(cfg config.Config, alias string) (ResolvedModel, error) {
	modelCfg, ok := cfg.Models[alias]
	if !ok {
		return ResolvedModel{}, fmt.Errorf("model alias %q not found", alias)
	}
	provCfg, ok := cfg.Providers[modelCfg.Provider]
	if !ok {
		return ResolvedModel{}, fmt.Errorf("provider %q not found for model %q", modelCfg.Provider, alias)
	}

	limits := resolveEffectiveLimits(modelCfg.Advanced.Limits)

	return ResolvedModel{
		Alias:                     alias,
		ProviderAlias:             modelCfg.Provider,
		ProviderConfig:            provCfg,
		BackendModelID:            modelCfg.ID,
		EffectiveLimits:           limits,
		Params:                    modelCfg.Params,
		ExtraParams:               modelCfg.ExtraParams,
		ThinkingEnabled:           modelCfg.ThinkingEnabled,
		ThinkingDisableMarker:     modelCfg.ThinkingDisableMarker,
		ThinkingScaffoldInference: modelCfg.ThinkingScaffoldInference,
		ThinkingParams:            modelCfg.ThinkingParams,
		Prompts:                   modelCfg.Prompts,
		Retry:                     modelCfg.Retry,
		MetadataSource:            "config",
		Confidence:                "high",
	}, nil
}

// resolveEffectiveLimits derives runtime effective limits from the user-configured
// advanced limits, filling in missing values with sensible defaults based on
// known fields. When nothing is configured, uses fallback defaults.
func resolveEffectiveLimits(adv config.AdvancedLimitsConfig) EffectiveLimits {
	cw := adv.ContextWindow
	maxOut := adv.MaxOutputTokens
	maxIn := adv.MaxInputTokens
	reserve := adv.OutputReserveTokens
	safety := adv.SafetyMarginTokens
	summaryMax := adv.SummaryMaxTokens
	threshold := 0.70

	// Fallback when nothing is configured: use reasonable defaults
	if cw == 0 && maxOut == 0 && maxIn == 0 && reserve == 0 {
		cw = 32768
		maxOut = 4096
		reserve = 4096
		safety = 2048
		summaryMax = 4096
		return EffectiveLimits{
			ContextWindow:       cw,
			MaxInputTokens:      maxIn,
			MaxOutputTokens:     maxOut,
			OutputReserveTokens: reserve,
			SafetyMarginTokens:  safety,
			SummaryMaxTokens:    summaryMax,
			CompactionThreshold: threshold,
		}
	}

	// Derive missing values from what we know
	if maxOut == 0 {
		maxOut = 4096
	}
	if reserve == 0 {
		reserve = maxOut
	}
	if safety == 0 && cw > 0 {
		safety = 2048
	}
	if summaryMax == 0 && cw > 0 {
		summaryMax = min(cw/4, 8192)
		if summaryMax == 0 {
			summaryMax = 1024
		}
	}

	return EffectiveLimits{
		ContextWindow:       cw,
		MaxInputTokens:      maxIn,
		MaxOutputTokens:     maxOut,
		OutputReserveTokens: reserve,
		SafetyMarginTokens:  safety,
		SummaryMaxTokens:    summaryMax,
		CompactionThreshold: threshold,
	}
}
