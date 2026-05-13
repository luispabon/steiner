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
	if patch.ModelAlias != "" {
		if m, ok := cfg.Models[patch.ModelAlias]; ok {
			cfg.Model = m
		}
	}
	if patch.Model != nil {
		applyModelPatch(&cfg.Model, patch.Model)
	}
}

func newModelConfigBase(cfg Config) ModelConfig {
	if base, ok := cfg.Models["default"]; ok {
		return cloneModelConfig(base)
	}
	return cloneModelConfig(cfg.Model)
}

func cloneModelConfig(src ModelConfig) ModelConfig {
	dst := src
	dst.ExtraParams = copyStringAnyMap(src.ExtraParams)
	dst.Thinking.Params = copyStringAnyMap(src.Thinking.Params)
	return dst
}

func applyModelPatch(dst *ModelConfig, patch *modelPatch) {
	if patch.Type != nil {
		dst.Type = *patch.Type
	}
	if patch.BaseURL != nil {
		dst.BaseURL = *patch.BaseURL
	}
	if patch.APIKey != nil {
		dst.APIKey = *patch.APIKey
	}
	if patch.Model != nil {
		dst.Model = *patch.Model
	}
	if patch.ExtraParams != nil {
		dst.ExtraParams = copyStringAnyMap(*patch.ExtraParams)
	}
	if patch.MaxCompletionTokens != nil {
		dst.MaxCompletionTokens = *patch.MaxCompletionTokens
	}
	if patch.ContextSize != nil {
		dst.ContextSize = *patch.ContextSize
	}
	if patch.Retry != nil {
		applyRetryPatch(&dst.Retry, patch.Retry)
	}
	if patch.Compaction != nil {
		applyCompactionPatch(&dst.Compaction, patch.Compaction)
	}
	if patch.Prompts != nil {
		applyModelPromptsPatch(&dst.Prompts, patch.Prompts)
	}
	if patch.Thinking != nil {
		applyThinkingConfigPatch(&dst.Thinking, patch.Thinking)
	}
}

func applyThinkingConfigPatch(dst *ThinkingConfig, patch *thinkingConfigPatch) {
	if patch.Enabled != nil {
		dst.Enabled = *patch.Enabled
	}
	if patch.EnabledScaffoldInference != nil {
		dst.EnabledScaffoldInference = *patch.EnabledScaffoldInference
	}
	if patch.DisableMarker != nil {
		dst.DisableMarker = *patch.DisableMarker
	}
	if patch.Params != nil {
		dst.Params = copyStringAnyMap(*patch.Params)
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

func applyCompactionPatch(dst *CompactionConfig, patch *compactionPatch) {
	if patch.SafetyMarginTokens != nil {
		dst.SafetyMarginTokens = *patch.SafetyMarginTokens
	}
	if patch.SummaryMaxTokens != nil {
		dst.SummaryMaxTokens = *patch.SummaryMaxTokens
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
