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
		if cfg.Models.SubAgents == nil {
			cfg.Models.SubAgents = make(map[string]string)
		}
		for name, alias := range *patch.SubAgents {
			cfg.Models.SubAgents[name] = alias
		}
	}
	if patch.OneShot != nil {
		if cfg.Models.OneShot == nil {
			cfg.Models.OneShot = make(map[string]string)
		}
		for phase, alias := range *patch.OneShot {
			cfg.Models.OneShot[phase] = alias
		}
	}
	if patch.WorkflowHandoff != nil {
		if cfg.Models.WorkflowHandoff == nil {
			cfg.Models.WorkflowHandoff = make(map[string]string)
		}
		for name, alias := range *patch.WorkflowHandoff {
			cfg.Models.WorkflowHandoff[name] = alias
		}
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
