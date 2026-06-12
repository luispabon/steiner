package config

// applyPatch applies a config patch to the config.
func applyPatch(cfg *Config, patch configPatch) {
	if patch.CavemanMode != nil {
		cfg.CavemanMode = *patch.CavemanMode
	}
	if patch.HumanizerMode != nil {
		cfg.HumanizerMode = *patch.HumanizerMode
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
