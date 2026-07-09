package provider

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

// openAIReasoningFallbackEfforts is the conservative built-in set of
// reasoning efforts assumed for known reasoning-capable OpenAI/Codex model
// families when config does not declare supported_efforts. It intentionally
// omits "none" and "xhigh", which are only supported by specific newer
// model variants that Steiner cannot reliably detect from the model ID
// alone.
var openAIReasoningFallbackEfforts = []string{"minimal", "low", "medium", "high"}

// openAIReasoningFallbackFamilies lists model ID substrings that identify
// known reasoning-capable OpenAI/Codex model families for fallback
// capability discovery.
var openAIReasoningFallbackFamilies = []string{"gpt-5", "o1", "o3", "o4", "codex"}

// resolveReasoningCapabilities derives reasoning capabilities and the
// effective reasoning effort for a model from config and, for
// OpenAI/Codex-compatible providers, a curated fallback table. Config
// values always take precedence over fallback data.
func resolveReasoningCapabilities(reasoningCfg config.ReasoningConfig, providerType config.ProviderType, backendModelID string) (ReasoningCapabilities, string) {
	caps := ReasoningCapabilities{
		SupportedEfforts: copyStrings(reasoningCfg.SupportedEfforts),
		Source:           "config",
		Confidence:       "high",
	}

	if len(caps.SupportedEfforts) == 0 {
		if fallback, ok := openAIReasoningFallback(providerType, backendModelID); ok {
			caps.SupportedEfforts = fallback
			caps.Source = "fallback"
			caps.Confidence = "low"
		} else {
			caps.Source = "unknown"
			caps.Confidence = "unknown"
		}
	}

	effectiveEffort := strings.TrimSpace(reasoningCfg.Effort)
	return caps, effectiveEffort
}

// openAIReasoningFallback returns the conservative fallback reasoning
// efforts for known OpenAI/Codex reasoning-capable model families. It
// returns ok=false for other providers or unrecognized model families.
func openAIReasoningFallback(providerType config.ProviderType, backendModelID string) (efforts []string, ok bool) {
	if providerType != config.ProviderTypeOpenAI && providerType != config.ProviderTypeCodex {
		return nil, false
	}
	modelID := strings.ToLower(strings.TrimSpace(backendModelID))
	for _, family := range openAIReasoningFallbackFamilies {
		if strings.Contains(modelID, family) {
			return copyStrings(openAIReasoningFallbackEfforts), true
		}
	}
	return nil, false
}

// ApplyReasoningOverride returns a copy of rm with a session-time reasoning
// override applied. A ReasoningOverrideProviderDefault override clears the
// effective effort even when config declared one. A ReasoningOverrideEffort
// override sets the effective effort when the value is present in
// rm.Reasoning.SupportedEfforts, or when SupportedEfforts is empty/unknown
// (in which case the value cannot be validated and is allowed through). It
// returns an error when SupportedEfforts is known and does not contain the
// requested effort.
func ApplyReasoningOverride(rm ResolvedModel, override ReasoningOverride) (ResolvedModel, error) {
	switch override.Kind {
	case ReasoningOverrideNone, "":
		return rm, nil
	case ReasoningOverrideProviderDefault:
		rm.ReasoningEffectiveEffort = ""
		return rm, nil
	case ReasoningOverrideEffort:
		effort := strings.TrimSpace(override.Effort)
		if len(rm.Reasoning.SupportedEfforts) > 0 && !containsString(rm.Reasoning.SupportedEfforts, effort) {
			return ResolvedModel{}, fmt.Errorf("apply reasoning override: effort %q is not supported for model %q", effort, rm.Alias)
		}
		rm.ReasoningEffectiveEffort = effort
		return rm, nil
	default:
		return ResolvedModel{}, fmt.Errorf("apply reasoning override: unknown override kind %q", override.Kind)
	}
}

func copyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
