package config

func applySchedulerConfigPatch(cfg *Config, patch configPatch) {
	if patch.Scheduler != nil {
		applySchedulerPatch(&cfg.Scheduler, patch.Scheduler)
	}
}

func applySchedulerPatch(dst *SchedulerConfig, patch *schedulerPatch) {
	if patch.Parallelism != nil {
		dst.Parallelism = *patch.Parallelism
	}
}

func applyModelConfigPatch(cfg *Config, patch configPatch) {
	if patch.DefaultModel != nil {
		cfg.DefaultModel = *patch.DefaultModel
	}
	if patch.Providers != nil {
		if cfg.Providers == nil {
			cfg.Providers = make(map[string]ProviderConfig)
		}
		for name, p := range *patch.Providers {
			current := cfg.Providers[name]
			applyProviderPatch(&current, &p)
			cfg.Providers[name] = current
		}
	}
	if patch.Models != nil {
		if cfg.Models == nil {
			cfg.Models = make(map[string]ModelConfig)
		}
		for name, model := range *patch.Models {
			current, ok := cfg.Models[name]
			if !ok {
				current = newModelConfigBase(*cfg)
			}
			applyModelPatch(&current, &model)
			cfg.Models[name] = current
		}
	}
}

func newModelConfigBase(cfg Config) ModelConfig {
	if base, ok := cfg.Models["default"]; ok {
		return cloneModelConfig(base)
	}
	if len(cfg.Models) > 0 {
		for _, m := range cfg.Models {
			return cloneModelConfig(m)
		}
	}
	return ModelConfig{}
}

func cloneModelConfig(src ModelConfig) ModelConfig {
	dst := src
	dst.Params = copyStringAnyMap(src.Params)
	dst.ExtraParams = copyStringAnyMap(src.ExtraParams)
	dst.ThinkingParams = copyStringAnyMap(src.ThinkingParams)
	return dst
}

func applyProviderPatch(dst *ProviderConfig, patch *providerPatch) {
	if patch.Type != nil {
		dst.Type = *patch.Type
	}
	if patch.BaseURL != nil {
		dst.BaseURL = *patch.BaseURL
	}
	if patch.APIKey != nil {
		dst.APIKey = *patch.APIKey
	}
	if patch.APIKeyEnv != nil {
		dst.APIKeyEnv = *patch.APIKeyEnv
	}
	if patch.Headers != nil {
		dst.Headers = *patch.Headers
	}
	if patch.Timeout != nil {
		dst.Timeout = *patch.Timeout
	}
}

func applyModelPatch(dst *ModelConfig, patch *modelPatch) {
	if patch.Provider != nil {
		dst.Provider = *patch.Provider
	}
	if patch.ID != nil {
		dst.ID = *patch.ID
	}
	if patch.Params != nil {
		dst.Params = copyStringAnyMap(*patch.Params)
	}
	if patch.ExtraParams != nil {
		dst.ExtraParams = copyStringAnyMap(*patch.ExtraParams)
	}
	if patch.ThinkingEnabled != nil {
		dst.ThinkingEnabled = *patch.ThinkingEnabled
	}
	if patch.ThinkingDisableMarker != nil {
		dst.ThinkingDisableMarker = *patch.ThinkingDisableMarker
	}
	if patch.ThinkingScaffoldInference != nil {
		dst.ThinkingScaffoldInference = *patch.ThinkingScaffoldInference
	}
	if patch.ThinkingParams != nil {
		dst.ThinkingParams = copyStringAnyMap(*patch.ThinkingParams)
	}
	if patch.Retry != nil {
		applyRetryPatch(&dst.Retry, patch.Retry)
	}
	if patch.Prompts != nil {
		applyModelPromptsPatch(&dst.Prompts, patch.Prompts)
	}
	if patch.Advanced != nil {
		applyAdvancedPatch(&dst.Advanced, patch.Advanced)
	}
}

func applyAdvancedPatch(dst *AdvancedConfig, patch *advancedPatch) {
	if patch.Limits != nil {
		applyAdvancedLimitsPatch(&dst.Limits, patch.Limits)
	}
}

func applyAdvancedLimitsPatch(dst *AdvancedLimitsConfig, patch *advancedLimitsPatch) {
	if patch.ContextWindow != nil {
		dst.ContextWindow = *patch.ContextWindow
	}
	if patch.MaxOutputTokens != nil {
		dst.MaxOutputTokens = *patch.MaxOutputTokens
	}
	if patch.MaxInputTokens != nil {
		dst.MaxInputTokens = *patch.MaxInputTokens
	}
	if patch.OutputReserveTokens != nil {
		dst.OutputReserveTokens = *patch.OutputReserveTokens
	}
	if patch.SafetyMarginTokens != nil {
		dst.SafetyMarginTokens = *patch.SafetyMarginTokens
	}
	if patch.SummaryMaxTokens != nil {
		dst.SummaryMaxTokens = *patch.SummaryMaxTokens
	}
}

func applyModelPromptsPatch(dst *ModelPrompts, patch *modelPromptsPatch) {
	if patch.System != nil {
		dst.System = *patch.System
	}
	if patch.Compaction != nil {
		dst.Compaction = *patch.Compaction
	}
}

func applyRetryPatch(dst *RetryConfig, patch *retryPatch) {
	if patch.Enabled != nil {
		dst.Enabled = *patch.Enabled
	}
	if patch.MaxAttempts != nil {
		dst.MaxAttempts = *patch.MaxAttempts
	}
	if patch.InitialBackoff != nil {
		dst.InitialBackoff = *patch.InitialBackoff
	}
	if patch.MaxBackoff != nil {
		dst.MaxBackoff = *patch.MaxBackoff
	}
	if patch.RetryAfterMax != nil {
		dst.RetryAfterMax = *patch.RetryAfterMax
	}
}
