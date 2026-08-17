package config

func setIfPresent[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

func applyTUIPatch(dst *TUIConfig, patch *tuiPatch) {
	if patch == nil {
		return
	}
	setIfPresent(&dst.FPS, patch.FPS)
}

func newModelConfigBase(cfg Config) ModelConfig {
	if base, ok := cfg.Models.Definitions["default"]; ok {
		cloned := cloneModelConfig(base)
		cloned.Advanced.Limits = AdvancedLimitsConfig{}
		return cloned
	}
	if len(cfg.Models.Definitions) > 0 {
		var first string
		for name := range cfg.Models.Definitions {
			if first == "" || name < first {
				first = name
			}
		}
		cloned := cloneModelConfig(cfg.Models.Definitions[first])
		cloned.Advanced.Limits = AdvancedLimitsConfig{}
		return cloned
	}
	return ModelConfig{}
}

func cloneModelConfig(src ModelConfig) ModelConfig {
	dst := src
	dst.Params = copyStringAnyMap(src.Params)
	dst.ExtraParams = copyStringAnyMap(src.ExtraParams)
	dst.Advanced.Reasoning.SupportedEfforts = copyStringSlice(src.Advanced.Reasoning.SupportedEfforts)
	return dst
}

func applyProviderPatch(dst *ProviderConfig, patch *providerPatch) {
	setIfPresent(&dst.Type, patch.Type)
	setIfPresent(&dst.BaseURL, patch.BaseURL)
	setIfPresent(&dst.APIKey, patch.APIKey)
	setIfPresent(&dst.APIKeyEnv, patch.APIKeyEnv)
	setIfPresent(&dst.Headers, patch.Headers)
	setIfPresent(&dst.Timeout, patch.Timeout)
	if patch.Codex != nil {
		applyCodexPatch(&dst.Codex, patch.Codex)
	}
}

func applyCodexPatch(dst *CodexConfig, patch *codexPatch) {
	setIfPresent(&dst.MinRequestInterval, patch.MinRequestInterval)
}

func applyModelPatch(dst *ModelConfig, patch *modelPatch) {
	setIfPresent(&dst.Provider, patch.Provider)
	setIfPresent(&dst.ID, patch.ID)
	if patch.Params != nil {
		dst.Params = copyStringAnyMap(*patch.Params)
	}
	if patch.ExtraParams != nil {
		dst.ExtraParams = copyStringAnyMap(*patch.ExtraParams)
	}
	setIfPresent(&dst.PromptSuffix, patch.PromptSuffix)
	if patch.Retry != nil {
		applyRetryPatch(&dst.Retry, patch.Retry)
	}
	if patch.Prompts != nil {
		applyModelPromptsPatch(&dst.Prompts, patch.Prompts)
	}
	if patch.Advanced != nil {
		applyAdvancedPatch(&dst.Advanced, patch.Advanced)
	}
	if patch.Vision != nil {
		vision := *patch.Vision
		dst.Vision = &vision
	}
}

func applyAdvancedPatch(dst *AdvancedConfig, patch *advancedPatch) {
	if patch.Limits != nil {
		applyAdvancedLimitsPatch(&dst.Limits, patch.Limits)
	}
	setIfPresent(&dst.Transport, patch.Transport)
	if patch.ReasoningEchoBack != nil {
		dst.ReasoningEchoBack = patch.ReasoningEchoBack
	}
	if patch.Reasoning != nil {
		applyReasoningPatch(&dst.Reasoning, patch.Reasoning)
	}
}

func applyAdvancedLimitsPatch(dst *AdvancedLimitsConfig, patch *advancedLimitsPatch) {
	setIfPresent(&dst.ContextWindow, patch.ContextWindow)
	setIfPresent(&dst.MaxOutputTokens, patch.MaxOutputTokens)
}

func applyReasoningPatch(dst *ReasoningConfig, patch *reasoningPatch) {
	setIfPresent(&dst.Effort, patch.Effort)
	if patch.SupportedEfforts != nil {
		dst.SupportedEfforts = copyStringSlice(*patch.SupportedEfforts)
	}
}

func applyModelPromptsPatch(dst *ModelPrompts, patch *modelPromptsPatch) {
	setIfPresent(&dst.System, patch.System)
	setIfPresent(&dst.Compaction, patch.Compaction)
	setIfPresent(&dst.SystemSuffix, patch.SystemSuffix)
}

func applyRetryPatch(dst *RetryConfig, patch *retryPatch) {
	setIfPresent(&dst.Enabled, patch.Enabled)
	setIfPresent(&dst.MaxAttempts, patch.MaxAttempts)
	setIfPresent(&dst.InitialBackoff, patch.InitialBackoff)
	setIfPresent(&dst.MaxBackoff, patch.MaxBackoff)
	setIfPresent(&dst.RetryAfterMax, patch.RetryAfterMax)
}
