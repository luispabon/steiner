package config

func applySubAgentPatch(dst *SubAgentConfig, patch *subAgentPatch) {
	setIfPresent(&dst.Enabled, patch.Enabled)
	setIfPresent(&dst.MaxTurns, patch.MaxTurns)
	setIfPresent(&dst.MaxTokens, patch.MaxTokens)
	setIfPresent(&dst.MaxParallel, patch.MaxParallel)
	setIfPresent(&dst.MaxFollowUps, patch.MaxFollowUps)
}

func applyAdvisorPatch(dst *AdvisorConfig, patch *advisorPatch) {
	setIfPresent(&dst.Enabled, patch.Enabled)
	setIfPresent(&dst.MaxUsesPerRun, patch.MaxUsesPerRun)
	setIfPresent(&dst.MaxUsesPerSubAgent, patch.MaxUsesPerSubAgent)
	if patch.MaxTokens != nil {
		value := *patch.MaxTokens
		dst.MaxTokens = &value
	}
	if patch.Timeout != nil {
		value := *patch.Timeout
		dst.Timeout = &value
	}
}

func applyOneShotPatch(dst *oneshotConfig, patch *oneshotPatch) {
	setIfPresent(&dst.AutoPR, patch.AutoPR)
}

func applyDesktopNotificationsPatch(dst *desktopNotificationsConfig, patch *desktopNotificationsPatch) {
	setIfPresent(&dst.Enabled, patch.Enabled)
	setIfPresent(&dst.Duration, patch.Duration)
}

func applyUpdateCheckPatch(dst *UpdateCheckConfig, patch *updateCheckPatch) {
	setIfPresent(&dst.Enabled, patch.Enabled)
	setIfPresent(&dst.IntervalHours, patch.IntervalHours)
}

func applyToolPatch(dst *ToolConfig, patch *toolPatch) {
	setIfPresent(&dst.Exec, patch.Exec)
	setIfPresent(&dst.Subcommand, patch.Subcommand)
	setIfPresent(&dst.Description, patch.Description)
	if patch.Parameters != nil {
		dst.Parameters = copyStringAnyMap(*patch.Parameters)
	}
	setIfPresent(&dst.Timeout, patch.Timeout)
}

func applySandboxPatch(cfg *SandboxConfig, patch *sandboxPatch) {
	setIfPresent(&cfg.Enabled, patch.Enabled)
	setIfPresent(&cfg.WarningOnUnsupportedPlatform, patch.WarningOnUnsupportedPlatform)
	if patch.EnvPassthrough != nil {
		cfg.EnvPassthrough = *patch.EnvPassthrough
	}
	setIfPresent(&cfg.EnvPassthroughAll, patch.EnvPassthroughAll)
	if patch.HostMounts != nil {
		cfg.HostMounts = *patch.HostMounts
	}
}

func applyPermissionsPatch(cfg *PermissionsConfig, patch *permissionsPatch) {
	setIfPresent(&cfg.Docker, patch.Docker)
}
