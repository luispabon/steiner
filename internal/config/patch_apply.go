package config

// applyPatch applies a config patch to the config.
func applyPatch(cfg *Config, patch configPatch) {
	if patch.CaveHuman != nil {
		cfg.CaveHuman = *patch.CaveHuman
	}
	applySchedulerConfigPatch(cfg, patch)
	applyModelConfigPatch(cfg, patch)
	applyLimitsConfigPatch(cfg, patch)
	applySubAgentConfigPatch(cfg, patch)
	applyToolConfigPatch(cfg, patch)
	applyProjectContextConfigPatch(cfg, patch)
	applyPathsConfigPatch(cfg, patch)
	applyLoggingConfigPatch(cfg, patch)
	applyContextManagementConfigPatch(cfg, patch)
	applySearchConfigPatch(cfg, patch)
}
