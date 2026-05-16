package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
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

// ResolveWithDiscovery resolves a model like Resolve but also attempts provider
// metadata discovery to fill in missing limits. Discovery is best-effort: any
// HTTP failure or unsupported provider type is silently ignored.
func ResolveWithDiscovery(cfg config.Config, alias string, httpClient *http.Client) (ResolvedModel, error) {
	rm, err := Resolve(cfg, alias)
	if err != nil {
		return ResolvedModel{}, err
	}

	modelCfg := cfg.Models[alias]
	if limitsFullyConfigured(modelCfg.Advanced.Limits) {
		return rm, nil
	}

	adv := modelCfg.Advanced.Limits

	// Try provider discovery.
	discoverer := NewDiscoverer(rm.ProviderConfig, httpClient)
	if discoverer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
		defer cancel()
		if meta, err := discoverer.DiscoverModelMetadata(ctx, rm.BackendModelID); err == nil {
			if meta.ContextWindow > 0 || meta.MaxOutputTokens > 0 {
				rm.EffectiveLimits = resolveEffectiveLimitsWithMeta(adv, meta)
				rm.MetadataSource = "discovery"
				rm.Confidence = "medium"
				return rm, nil
			}
		}
	}

	// Try models.dev cache as third source.
	cache := &metadata.Cache{Dir: metadataCacheDir(), HTTPClient: httpClient}
	if data, err := cache.Load(); err == nil && data != nil {
		info := metadata.Lookup(data, rm.BackendModelID)
		if info.ContextWindow > 0 || info.MaxOutputTokens > 0 {
			meta := ModelMetadata{ContextWindow: info.ContextWindow, MaxOutputTokens: info.MaxOutputTokens}
			rm.EffectiveLimits = resolveEffectiveLimitsWithMeta(adv, meta)
			rm.MetadataSource = "models.dev"
			rm.Confidence = "medium"
			return rm, nil
		}
	}

	// Check if we ended up using fallback defaults
	if isFallbackLimits(adv) {
		rm.MetadataSource = "fallback"
		rm.Confidence = "low"
		rm.Warnings = append(rm.Warnings, fmt.Sprintf(
			"Model metadata warning: %s/%s has unknown context limits. Using conservative fallback: context_window=%d, max_output_tokens=%d. Set models.%s.advanced.limits.context_window to remove this warning.",
			alias, rm.BackendModelID, rm.EffectiveLimits.ContextWindow, rm.EffectiveLimits.MaxOutputTokens, alias,
		))
	}

	return rm, nil
}

// metadataCacheDir returns the directory for the models.dev metadata cache,
// honouring $XDG_CACHE_HOME when set.
func metadataCacheDir() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "steiner", "model-metadata")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "steiner", "model-metadata")
}

// limitsFullyConfigured reports whether both context window and max output
// tokens are explicitly configured, making discovery unnecessary.
func limitsFullyConfigured(adv config.AdvancedLimitsConfig) bool {
	return adv.ContextWindow > 0 && adv.MaxOutputTokens > 0
}

// isFallbackLimits reports whether the advanced limits are all zero, indicating
// that fallback defaults will be used.
func isFallbackLimits(adv config.AdvancedLimitsConfig) bool {
	return adv.ContextWindow == 0 && adv.MaxOutputTokens == 0 && adv.MaxInputTokens == 0 && adv.OutputReserveTokens == 0
}

// resolveEffectiveLimitsWithMeta merges discovered metadata with user-configured
// limits. User-configured values always take precedence; discovered values fill
// gaps where the user has not set a value.
func resolveEffectiveLimitsWithMeta(adv config.AdvancedLimitsConfig, meta ModelMetadata) EffectiveLimits {
	if adv.ContextWindow == 0 && meta.ContextWindow > 0 {
		adv.ContextWindow = meta.ContextWindow
	}
	if adv.MaxOutputTokens == 0 && meta.MaxOutputTokens > 0 {
		adv.MaxOutputTokens = meta.MaxOutputTokens
	}
	return resolveEffectiveLimits(adv)
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
