package provider

import (
	"context"
	"fmt"
	"net/http"

	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
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
	reasoningCaps, reasoningEffectiveEffort := resolveReasoningCapabilities(modelCfg.Advanced.Reasoning)

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

	ref := modelRef{
		Alias:          reference,
		IsAlias:        isAlias,
		ProviderAlias:  modelCfg.Provider,
		Provider:       provCfg,
		Profile:        profileFor(provCfg.Type),
		BackendModelID: modelCfg.ID,
		ModelConfig:    modelCfg,
	}

	if !useDiscovery {
		facts, _, _ := resolveFacts(context.Background(), ref, []factSource{configSource{}, providerFixedSource{}, builtinSource{}})
		applyFacts(&rm, facts, false)
		return rm, nil
	}

	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir(), HTTPClient: httpClient}
	cacheCtx, cacheCancel := context.WithTimeout(context.Background(), discoveryTimeout)
	loadResult := cache.LoadBestEffortWithStatus(cacheCtx)
	cacheCancel()

	sources := []factSource{
		configSource{}, providerFixedSource{}, probeSource{httpClient: httpClient},
		modelsDevSource{data: loadResult.Data, loadReason: loadResult.Status.Reason},
		builtinSource{},
	}
	facts, notes, sourceErrs := resolveFacts(context.Background(), ref, sources)
	applyFacts(&rm, facts, true)
	rm.Warnings = append(rm.Warnings, deriveWarnings(ref, facts, notes, sourceErrs)...)
	return rm, nil
}
