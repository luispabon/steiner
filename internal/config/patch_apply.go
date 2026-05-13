package config

// applyPatch applies a config patch to the config.
func applyPatch(cfg *Config, patch configPatch) {
	applySchedulerConfigPatch(cfg, patch)
	applyModelConfigPatch(cfg, patch)
	applyLimitsConfigPatch(cfg, patch)
	applyApprovalConfigPatch(cfg, patch)
	applySubAgentConfigPatch(cfg, patch)
	applyToolConfigPatch(cfg, patch)
	applyProjectContextConfigPatch(cfg, patch)
	applyPathsConfigPatch(cfg, patch)
	applyLoggingConfigPatch(cfg, patch)
	applyDebugConfigPatch(cfg, patch)
	applyContextManagementConfigPatch(cfg, patch)
}
