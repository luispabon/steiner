package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/tiktoken-go/tokenizer"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

// EffectiveLimits holds the runtime-resolved token limits for a model.
type EffectiveLimits struct {
	ContextWindow             int
	MaxOutputTokens           int
	CompactionThreshold       float64
	EstimatorPadTokens        int
	NormalSummaryMaxTokens    int
	EmergencySummaryMaxTokens int
}

// TransportType identifies which request transport Steiner should use.
type TransportType string

const (
	// TransportConfigured uses the configured provider transport as-is.
	TransportConfigured TransportType = "configured"
	// TransportOpenAICompat uses OpenAI-compatible request transport.
	TransportOpenAICompat TransportType = "openai_compat"
	// TransportAnthropic uses Anthropic-native request transport.
	TransportAnthropic TransportType = "anthropic"
)

// ResolvedModel is the runtime object combining provider and model config
// with resolved metadata.
type ResolvedModel struct {
	Alias                     string
	ProviderAlias             string
	ProviderConfig            config.ProviderConfig
	BackendModelID            string
	EffectiveProviderType     config.ProviderType
	EffectiveTransport        TransportType
	EffectiveLimits           EffectiveLimits
	Params                    map[string]any
	ExtraParams               map[string]any
	PromptSuffix              string
	ReasoningEchoBack         bool
	Prompts                   config.ModelPrompts
	Retry                     config.RetryConfig
	MetadataSource            string
	Confidence                string
	TokenizerStrategy         string
	TokenizerConfidence       string
	TransportOverrideReason   string
	Vision                    *bool
	Reasoning                 ReasoningCapabilities
	ReasoningConfiguredEffort string
	ReasoningEffectiveEffort  string
	Warnings                  []string
}

// Resolve builds a ResolvedModel from cfg for the given model alias or raw
// provider/model-id reference.
func Resolve(cfg config.Config, alias string) (ResolvedModel, error) {
	return resolveReference(&cfg, alias, false, nil)
}

// ResolveWithDiscovery resolves a model like Resolve but also attempts provider
// metadata discovery to fill in missing limits. Discovery is best-effort: any
// HTTP failure or unsupported provider type is silently ignored.
func ResolveWithDiscovery(cfg config.Config, alias string, httpClient *http.Client) (ResolvedModel, error) {
	return resolveReference(&cfg, alias, true, httpClient)
}

// ResolveReasoningBatch resolves reasoning capabilities and effective efforts for
// every configured model alias in a single pass, loading models.dev metadata once
// instead of once per alias. Resolution failures for a given alias are skipped
// rather than surfaced, so one misconfigured model does not block the rest.
func ResolveReasoningBatch(cfg config.Config, httpClient *http.Client) (
	map[string]ReasoningCapabilities, map[string]string,
) {
	if len(cfg.Models.Definitions) == 0 {
		return nil, nil
	}

	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir(), HTTPClient: httpClient}
	cacheCtx, cacheCancel := context.WithTimeout(context.Background(), discoveryTimeout)
	defer cacheCancel()
	var data []byte
	if d, err := cache.LoadBestEffort(cacheCtx); err == nil {
		data = d
	}

	caps := make(map[string]ReasoningCapabilities)
	efforts := make(map[string]string)

	for alias, modelCfg := range cfg.Models.Definitions {
		rm, err := Resolve(cfg, alias)
		if err != nil {
			continue
		}

		loadAndApplyModelsDevMetadataFromData(&rm, modelCfg, data)
		caps[alias] = rm.Reasoning
		efforts[alias] = rm.ReasoningEffectiveEffort
	}

	return caps, efforts
}

// loadAndApplyModelsDevMetadataFromData applies transport, echo back, vision,
// and reasoning effort overrides from already-loaded models.dev data. Returns
// the looked-up metadata for downstream limit resolution.
func loadAndApplyModelsDevMetadataFromData(rm *ResolvedModel, modelCfg config.ModelConfig, data []byte) metadata.ModelInfo {
	var info metadata.ModelInfo
	if data != nil {
		info = metadata.LookupWithProvider(data, rm.ProviderAlias, rm.BackendModelID)
	}
	rm.EffectiveProviderType, rm.EffectiveTransport, rm.TransportOverrideReason = resolveEffectiveTransport(
		modelCfg.Advanced.Transport, rm.ProviderConfig.Type, info, rm.BackendModelID,
	)
	if modelCfg.Advanced.ReasoningEchoBack == nil {
		rm.ReasoningEchoBack = info.ReasoningEchoBack
	}
	if modelCfg.Vision == nil && info.Found {
		v := info.VisionInput
		rm.Vision = &v
	}
	if rm.Reasoning.Source != "config" && len(info.ReasoningSupportedEfforts) > 0 {
		rm.Reasoning = ReasoningCapabilities{
			SupportedEfforts: info.ReasoningSupportedEfforts,
			Source:           "models.dev",
			Confidence:       "medium",
		}
	}
	return info
}

// loadAndApplyModelsDevMetadata loads models.dev metadata and applies transport,
// echo back, vision, and reasoning effort overrides to rm. Returns the loaded
// metadata for downstream limit resolution.
func loadAndApplyModelsDevMetadata(rm *ResolvedModel, modelCfg config.ModelConfig, httpClient *http.Client) metadata.ModelInfo {
	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir(), HTTPClient: httpClient}
	cacheCtx, cacheCancel := context.WithTimeout(context.Background(), discoveryTimeout)
	defer cacheCancel()
	var data []byte
	if d, err := cache.LoadBestEffort(cacheCtx); err == nil {
		data = d
	}
	return loadAndApplyModelsDevMetadataFromData(rm, modelCfg, data)
}

// resolveLimitsFromDiscovery attempts to fill in missing token limits via
// provider discovery, models.dev metadata, or conservative fallback defaults.
func resolveLimitsFromDiscovery(rm *ResolvedModel, adv config.AdvancedLimitsConfig, modelsDevInfo metadata.ModelInfo, httpClient *http.Client, isAlias bool, reference string) {
	discoverer := NewDiscoverer(rm.ProviderConfig, httpClient)
	if discoverer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
		defer cancel()
		if meta, err := discoverer.DiscoverModelMetadata(ctx, rm.BackendModelID); err == nil {
			if meta.ContextWindow > 0 || meta.MaxOutputTokens > 0 {
				rm.EffectiveLimits = resolveEffectiveLimitsWithMeta(adv, meta)
				rm.MetadataSource = "discovery"
				rm.Confidence = "medium"
				return
			}
		}
	}

	if modelsDevInfo.ContextWindow > 0 || modelsDevInfo.MaxOutputTokens > 0 {
		meta := ModelMetadata{ContextWindow: modelsDevInfo.ContextWindow, MaxOutputTokens: modelsDevInfo.MaxOutputTokens}
		rm.EffectiveLimits = resolveEffectiveLimitsWithMeta(adv, meta)
		rm.MetadataSource = "models.dev"
		rm.Confidence = "medium"
		return
	}

	if isFallbackLimits(adv) {
		rm.MetadataSource = "fallback"
		rm.Confidence = "low"
		if isAlias {
			rm.Warnings = append(rm.Warnings, fmt.Sprintf(
				"Model metadata warning: %s/%s has unknown context limits. Using conservative fallback: context_window=%d, max_output_tokens=%d. Set models.%s.advanced.limits.context_window to remove this warning.",
				rm.Alias, rm.BackendModelID, rm.EffectiveLimits.ContextWindow, rm.EffectiveLimits.MaxOutputTokens, rm.Alias,
			))
		} else {
			rm.Warnings = append(rm.Warnings, fmt.Sprintf(
				"Model metadata warning: %s has unknown context limits. Using conservative fallback: context_window=%d, max_output_tokens=%d.",
				reference, rm.EffectiveLimits.ContextWindow, rm.EffectiveLimits.MaxOutputTokens,
			))
		}
	}
}

func resolveEffectiveTransport(override config.ModelTransportType, configuredType config.ProviderType, info metadata.ModelInfo, backendID string) (config.ProviderType, TransportType, string) {
	switch override {
	case config.ModelTransportOpenAICompat:
		return config.ProviderTypeOpenAICompat, TransportOpenAICompat, "explicit config override"
	case config.ModelTransportAnthropic:
		return config.ProviderTypeAnthropic, TransportAnthropic, "explicit config override"
	}

	if configuredType == config.ProviderTypeCodex {
		return configuredType, TransportConfigured, "codex provider uses OAuth Responses transport"
	}

	switch metadataProviderTransport(info) {
	case TransportAnthropic:
		return config.ProviderTypeAnthropic, TransportAnthropic, "models.dev provider override for " + backendID
	case TransportOpenAICompat:
		return config.ProviderTypeOpenAICompat, TransportOpenAICompat, "models.dev provider override for " + backendID
	default:
		return configuredType, TransportConfigured, "none"
	}
}

func metadataProviderTransport(info metadata.ModelInfo) TransportType {
	for _, npm := range []string{info.ModelProviderNPM, info.ProviderNPM} {
		switch strings.TrimSpace(npm) {
		case "@ai-sdk/anthropic":
			return TransportAnthropic
		case "@ai-sdk/openai-compatible":
			return TransportOpenAICompat
		}
	}
	return TransportConfigured
}

func resolveProviderConfig(cfg config.ProviderConfig) config.ProviderConfig {
	resolved := cfg
	if strings.TrimSpace(resolved.BaseURL) == "" {
		resolved.BaseURL = defaultProviderBaseURL(resolved.Type)
	}
	if strings.TrimSpace(resolved.APIKey) == "" && strings.TrimSpace(resolved.APIKeyEnv) != "" {
		resolved.APIKey = os.Getenv(strings.TrimSpace(resolved.APIKeyEnv))
	}
	if len(resolved.Headers) > 0 {
		cloned := make(map[string]string, len(resolved.Headers))
		for key, value := range resolved.Headers {
			cloned[key] = value
		}
		resolved.Headers = cloned
	}
	if resolved.Type == config.ProviderTypeCodex && resolved.Codex.MinRequestInterval.IsUnset() {
		resolved.Codex.MinRequestInterval = config.DefaultCodexMinRequestInterval
	}
	return resolved
}

func defaultProviderBaseURL(providerType config.ProviderType) string {
	switch providerType {
	case config.ProviderTypeOpenRouter:
		return "https://openrouter.ai/api/v1"
	case config.ProviderTypeOpenAI:
		return "https://api.openai.com/v1"
	case config.ProviderTypeCodex:
		return "https://api.openai.com/v1"
	default:
		return ""
	}
}

// limitsFullyConfigured reports whether both context window and max output
// tokens are explicitly configured, making discovery unnecessary.
func limitsFullyConfigured(adv config.AdvancedLimitsConfig) bool {
	return adv.ContextWindow > 0 && adv.MaxOutputTokens > 0
}

// isFallbackLimits reports whether the advanced limits are all zero, indicating
// that fallback defaults will be used.
func isFallbackLimits(adv config.AdvancedLimitsConfig) bool {
	return adv.ContextWindow == 0 && adv.MaxOutputTokens == 0
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
	if cw == 0 && maxOut == 0 {
		cw = 32768
		maxOut = 4096
	}
	return deriveEffectiveLimits(cw, maxOut)
}

func deriveEffectiveLimits(contextWindow, maxOutputTokens int) EffectiveLimits {
	return EffectiveLimits{
		ContextWindow:             contextWindow,
		MaxOutputTokens:           maxOutputTokens,
		CompactionThreshold:       0.70,
		EstimatorPadTokens:        clampInt(contextWindow/100, 256, 2048),
		NormalSummaryMaxTokens:    deriveSummaryMaxTokens(contextWindow, maxOutputTokens, 8, 4096, 16000),
		EmergencySummaryMaxTokens: deriveSummaryMaxTokens(contextWindow, maxOutputTokens, 4, 2048, 8000),
	}
}

func deriveSummaryMaxTokens(contextWindow, maxOutputTokens, percent, minTokens, maxTokens int) int {
	derived := clampInt(contextWindow*percent/100, minTokens, maxTokens)
	if maxOutputTokens > 0 {
		return minInt(maxOutputTokens, derived)
	}
	return derived
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func resolveTokenizerMetadata(modelID string) (strategy string, confidence string) {
	return resolveTokenizerMetadataWithLoader(modelID, tokenizerForModel)
}

func resolveTokenizerMetadataWithLoader(modelID string, loadTokenizer func(string) (tokenizer.Codec, error)) (strategy string, confidence string) {
	modelID = strings.TrimSpace(modelID)
	if _, err := loadTokenizer(modelID); err != nil {
		return TokenizerStrategyHeuristic, "low"
	}

	if tokenizerMatchConfidence(modelID, encodingNameForModel(modelID)) == "high" {
		return TokenizerStrategyTiktoken, "high"
	}
	return TokenizerStrategyTiktoken, "low"
}

func tokenizerMatchConfidence(modelID string, encoding tokenizer.Encoding) string {
	switch encoding {
	case tokenizer.O200kBase:
		if hasAnyPrefix(modelID, "gpt-4.5", "gpt-4.1", "gpt-4o", "o1", "o3") {
			return "high"
		}
	case tokenizer.Cl100kBase:
		if hasAnyPrefix(modelID, "gpt-4", "gpt-3.5", "text-embedding-ada-002", "text-embedding-3") {
			return "high"
		}
	case tokenizer.P50kBase:
		if hasAnyPrefix(modelID, "text-davinci", "code-davinci", "code-cushman") {
			return "high"
		}
	case tokenizer.R50kBase:
		if modelID == "gpt2" || hasAnyPrefix(modelID, "davinci", "curie", "babbage", "ada") {
			return "high"
		}
	}
	return "low"
}

func hasAnyPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
