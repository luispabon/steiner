package config

// applyPatch applies a config patch to the config.
func applyPatch(cfg *Config, patch configPatch) {
	applyCoreConfigPatch(cfg, patch)
	applyRuntimeConfigPatch(cfg, patch)
	applyToolingConfigPatch(cfg, patch)
}

func applyCoreConfigPatch(cfg *Config, patch configPatch) {
	if patch.CaveHuman != nil {
		cfg.CaveHuman = *patch.CaveHuman
	}
	if patch.Scheduler != nil {
		applySchedulerPatch(&cfg.Scheduler, patch.Scheduler)
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
		applyModelsPatch(cfg, patch.Models)
	}
	if patch.Modes != nil {
		applyModesPatch(&cfg.Modes, patch.Modes)
	}
}

func applyModelsPatch(cfg *Config, patch *modelsPatch) {
	if patch.Default != nil {
		cfg.Models.Default = *patch.Default
	}
	if patch.Definitions != nil {
		if cfg.Models.Definitions == nil {
			cfg.Models.Definitions = make(map[string]ModelConfig)
		}
		for name, model := range *patch.Definitions {
			current, ok := cfg.Models.Definitions[name]
			if !ok {
				current = newModelConfigBase(*cfg)
			}
			applyModelPatch(&current, &model)
			cfg.Models.Definitions[name] = current
		}
	}
	if patch.Advisor != nil {
		cfg.Models.Advisor = *patch.Advisor
	}
	if patch.SubAgents != nil {
		mergeStringMapPatch(&cfg.Models.SubAgents, *patch.SubAgents)
	}
	if patch.OneShot != nil {
		mergeStringMapPatch(&cfg.Models.OneShot, *patch.OneShot)
	}
	if patch.WorkflowHandoff != nil {
		mergeStringMapPatch(&cfg.Models.WorkflowHandoff, *patch.WorkflowHandoff)
	}
}

// mergeStringMapPatch merges patch into dst, initializing dst if nil.
func mergeStringMapPatch(dst *map[string]string, patch map[string]string) {
	if *dst == nil {
		*dst = make(map[string]string)
	}
	for k, v := range patch {
		(*dst)[k] = v
	}
}

func applyRuntimeConfigPatch(cfg *Config, patch configPatch) {
	if patch.Limits != nil {
		applyLimitsPatch(&cfg.Limits, patch.Limits)
	}
	if patch.SubAgent != nil {
		applySubAgentPatch(&cfg.SubAgent, patch.SubAgent)
	}
	if patch.Advisor != nil {
		applyAdvisorPatch(&cfg.Advisor, patch.Advisor)
	}
	if patch.OneShot != nil {
		applyOneShotPatch(&cfg.OneShot, patch.OneShot)
	}
	if patch.DesktopNotifications != nil {
		applyDesktopNotificationsPatch(&cfg.DesktopNotifications, patch.DesktopNotifications)
	}
}

func applyToolingConfigPatch(cfg *Config, patch configPatch) {
	if patch.Tools != nil {
		if cfg.Tools == nil {
			cfg.Tools = make(map[string]ToolConfig)
		}
		for name, tool := range *patch.Tools {
			current := cfg.Tools[name]
			applyToolPatch(&current, &tool)
			cfg.Tools[name] = current
		}
	}
	if patch.ProjectContext != nil {
		applyProjectContextPatch(&cfg.ProjectContext, patch.ProjectContext)
	}
	if patch.Paths != nil {
		applyPathsPatch(&cfg.Paths, patch.Paths)
	}
	if patch.Logging != nil {
		applyLoggingPatch(&cfg.Logging, patch.Logging)
	}
	if patch.ContextManagement != nil {
		applyContextManagementPatch(&cfg.ContextManagement, patch.ContextManagement)
	}
	if patch.Search != nil {
		applySearchPatch(&cfg.Search, patch.Search)
	}
}
