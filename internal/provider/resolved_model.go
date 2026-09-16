package provider

import (
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
	Facts                     ModelFacts
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

// ResolveProviderConfig applies runtime defaults and environment-backed credentials.
func ResolveProviderConfig(cfg config.ProviderConfig) config.ProviderConfig {
	resolved := cfg
	if strings.TrimSpace(resolved.BaseURL) == "" {
		resolved.BaseURL = profileFor(resolved.Type).DefaultBaseURL
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

const (
	defaultContextWindow   = 32768
	defaultMaxOutputTokens = 4096
)

// resolveEffectiveLimits derives runtime effective limits from the user-configured
// advanced limits, filling in missing values with sensible defaults based on
// known fields. When nothing is configured, uses fallback defaults.
func resolveEffectiveLimits(adv config.AdvancedLimitsConfig) EffectiveLimits {
	cw := adv.ContextWindow
	maxOut := adv.MaxOutputTokens
	if cw == 0 && maxOut == 0 {
		cw = defaultContextWindow
		maxOut = defaultMaxOutputTokens
	}
	return deriveEffectiveLimits(cw, maxOut)
}

func deriveEffectiveLimits(contextWindow, maxOutputTokens int) EffectiveLimits {
	return EffectiveLimits{
		ContextWindow:             contextWindow,
		MaxOutputTokens:           maxOutputTokens,
		CompactionThreshold:       0.70,
		EstimatorPadTokens:        clampInt(contextWindow/100, 256, 2048),
		NormalSummaryMaxTokens:    deriveSummaryMaxTokens(contextWindow, maxOutputTokens, 8, 6144, 20480),
		EmergencySummaryMaxTokens: deriveSummaryMaxTokens(contextWindow, maxOutputTokens, 4, 3072, 10240),
	}
}

func deriveSummaryMaxTokens(contextWindow, maxOutputTokens, percent, minTokens, maxTokens int) int {
	derived := clampInt(contextWindow*percent/100, minTokens, maxTokens)
	if maxOutputTokens > 0 {
		return min(maxOutputTokens, derived)
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
		if hasAnyPrefix(modelID, "gpt-5", "gpt-4.5", "gpt-4.1", "gpt-4o", "o1", "o3") {
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
