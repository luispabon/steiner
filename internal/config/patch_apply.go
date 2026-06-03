package config

// applyPatch applies a config patch to the config.
func applyPatch(cfg *Config, patch configPatch) {
	if patch.CavemanMode != nil {
		cfg.CavemanMode = *patch.CavemanMode
	}
	applySchedulerConfigPatch(cfg, patch)
	applyModelConfigPatch(cfg, patch)
	applyLimitsConfigPatch(cfg, patch)
	applyApprovalConfigPatch(cfg, patch)
	applySubAgentConfigPatch(cfg, patch)
	applyToolConfigPatch(cfg, patch)
	applyProjectContextConfigPatch(cfg, patch)
	applyPathsConfigPatch(cfg, patch)
	applyLoggingConfigPatch(cfg, patch)
	applyContextManagementConfigPatch(cfg, patch)
	applySearchConfigPatch(cfg, patch)
}
