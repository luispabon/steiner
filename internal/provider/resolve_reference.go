package provider

import (
	"fmt"
	"net/http"

	"strings"

	"github.com/luispabon/steiner/internal/config"
)

// resolveReference resolves a configured model alias or a raw provider/model-id
// reference and optionally fills missing runtime metadata through discovery.
func resolveReference(cfg *config.Config, reference string, useDiscovery bool, httpClient *http.Client) (ResolvedModel, error) {
	modelCfg, isAlias := config.ResolveModelConfig(cfg, reference)
	if !isAlias && (modelCfg.Provider == "" || modelCfg.ID == "") {
		return ResolvedModel{}, fmt.Errorf("model alias %q not found", reference)
	}

	provCfg, ok := cfg.Providers[modelCfg.Provider]
	if !ok {
		return ResolvedModel{}, fmt.Errorf("provider %q not found for model %q", modelCfg.Provider, reference)
	}
	provCfg = ResolveProviderConfig(provCfg)

	limits := resolveEffectiveLimits(modelCfg.Advanced.Limits)
	tokenizerStrategy, tokenizerConfidence := resolveTokenizerMetadata(modelCfg.ID)
	reasoningCaps, reasoningEffectiveEffort := resolveReasoningCapabilities(modelCfg.Advanced.Reasoning, provCfg.Type, modelCfg.ID)

	rm := ResolvedModel{
		Alias:                     reference,
		ProviderAlias:             modelCfg.Provider,
		ProviderConfig:            provCfg,
		BackendModelID:            modelCfg.ID,
		EffectiveProviderType:     provCfg.Type,
		EffectiveTransport:        TransportConfigured,
		EffectiveLimits:           limits,
		Params:                    modelCfg.Params,
		ExtraParams:               modelCfg.ExtraParams,
		PromptSuffix:              modelCfg.PromptSuffix,
		Prompts:                   modelCfg.Prompts,
		Retry:                     modelCfg.Retry,
		MetadataSource:            "config",
		Confidence:                "high",
		TokenizerStrategy:         tokenizerStrategy,
		TokenizerConfidence:       tokenizerConfidence,
		Vision:                    modelCfg.Vision,
		Reasoning:                 reasoningCaps,
		ReasoningConfiguredEffort: strings.TrimSpace(modelCfg.Advanced.Reasoning.Effort),
		ReasoningEffectiveEffort:  reasoningEffectiveEffort,
	}
	if modelCfg.Advanced.ReasoningEchoBack != nil {
		rm.ReasoningEchoBack = *modelCfg.Advanced.ReasoningEchoBack
	}

	if !useDiscovery {
		return rm, nil
	}

	adv := modelCfg.Advanced.Limits
	modelsDevInfo := loadAndApplyModelsDevMetadata(&rm, modelCfg, httpClient)
	if limitsFullyConfigured(adv) {
		return rm, nil
	}
	resolveLimitsFromDiscovery(&rm, adv, modelsDevInfo, httpClient, isAlias, reference)
	return rm, nil
}
