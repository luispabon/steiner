package config

func setIfPresent[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

func applySchedulerConfigPatch(cfg *Config, patch configPatch) {
	if patch.Scheduler != nil {
		applySchedulerPatch(&cfg.Scheduler, patch.Scheduler)
	}
}

func applySchedulerPatch(dst *SchedulerConfig, patch *schedulerPatch) {
	setIfPresent(&dst.Parallelism, patch.Parallelism)
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
		cloned := cloneModelConfig(base)
		cloned.Advanced.Limits = AdvancedLimitsConfig{}
		return cloned
	}
	if len(cfg.Models) > 0 {
		for _, m := range cfg.Models {
			cloned := cloneModelConfig(m)
			cloned.Advanced.Limits = AdvancedLimitsConfig{}
			return cloned
		}
	}
	return ModelConfig{}
}

func cloneModelConfig(src ModelConfig) ModelConfig {
	dst := src
	dst.Params = copyStringAnyMap(src.Params)
	dst.ExtraParams = copyStringAnyMap(src.ExtraParams)
	return dst
}

func applyProviderPatch(dst *ProviderConfig, patch *providerPatch) {
	setIfPresent(&dst.Type, patch.Type)
	setIfPresent(&dst.BaseURL, patch.BaseURL)
	setIfPresent(&dst.APIKey, patch.APIKey)
	setIfPresent(&dst.APIKeyEnv, patch.APIKeyEnv)
	setIfPresent(&dst.Headers, patch.Headers)
	setIfPresent(&dst.Timeout, patch.Timeout)
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
}

func applyAdvancedPatch(dst *AdvancedConfig, patch *advancedPatch) {
	if patch.Limits != nil {
		applyAdvancedLimitsPatch(&dst.Limits, patch.Limits)
	}
	setIfPresent(&dst.Transport, patch.Transport)
}

func applyAdvancedLimitsPatch(dst *AdvancedLimitsConfig, patch *advancedLimitsPatch) {
	setIfPresent(&dst.ContextWindow, patch.ContextWindow)
	setIfPresent(&dst.MaxOutputTokens, patch.MaxOutputTokens)
}

func applyModelPromptsPatch(dst *ModelPrompts, patch *modelPromptsPatch) {
	setIfPresent(&dst.System, patch.System)
	setIfPresent(&dst.Compaction, patch.Compaction)
}

func applyRetryPatch(dst *RetryConfig, patch *retryPatch) {
	setIfPresent(&dst.Enabled, patch.Enabled)
	setIfPresent(&dst.MaxAttempts, patch.MaxAttempts)
	setIfPresent(&dst.InitialBackoff, patch.InitialBackoff)
	setIfPresent(&dst.MaxBackoff, patch.MaxBackoff)
	setIfPresent(&dst.RetryAfterMax, patch.RetryAfterMax)
}
