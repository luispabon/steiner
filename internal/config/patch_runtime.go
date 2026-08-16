package config

func applySubAgentPatch(dst *SubAgentConfig, patch *subAgentPatch) {
	if patch.Enabled != nil {
		dst.Enabled = *patch.Enabled
	}
	if patch.MaxTurns != nil {
		dst.MaxTurns = *patch.MaxTurns
	}
	if patch.MaxTokens != nil {
		dst.MaxTokens = *patch.MaxTokens
	}
}

func applyAdvisorPatch(dst *AdvisorConfig, patch *advisorPatch) {
	if patch.Enabled != nil {
		dst.Enabled = *patch.Enabled
	}
	if patch.MaxUsesPerRun != nil {
		dst.MaxUsesPerRun = *patch.MaxUsesPerRun
	}
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
	if patch.AutoPR != nil {
		dst.AutoPR = *patch.AutoPR
	}
}

func applyDesktopNotificationsPatch(dst *desktopNotificationsConfig, patch *desktopNotificationsPatch) {
	if patch.Enabled != nil {
		dst.Enabled = *patch.Enabled
	}
	if patch.Duration != nil {
		dst.Duration = *patch.Duration
	}
}

func applyToolPatch(dst *ToolConfig, patch *toolPatch) {
	if patch.Exec != nil {
		dst.Exec = *patch.Exec
	}
	if patch.Subcommand != nil {
		dst.Subcommand = *patch.Subcommand
	}
	if patch.Description != nil {
		dst.Description = *patch.Description
	}
	if patch.Parameters != nil {
		dst.Parameters = copyStringAnyMap(*patch.Parameters)
	}
	if patch.Timeout != nil {
		dst.Timeout = *patch.Timeout
	}
	if patch.Constraints != nil {
		dst.Constraints = copyStringAnyMap(*patch.Constraints)
	}
}

func applySandboxPatch(cfg *SandboxConfig, patch *sandboxPatch) {
	if patch.Enabled != nil {
		cfg.Enabled = *patch.Enabled
	}
	if patch.WarningOnUnsupportedPlatform != nil {
		cfg.WarningOnUnsupportedPlatform = *patch.WarningOnUnsupportedPlatform
	}
	if patch.EnvPassthrough != nil {
		cfg.EnvPassthrough = *patch.EnvPassthrough
	}
	if patch.EnvPassthroughAll != nil {
		cfg.EnvPassthroughAll = *patch.EnvPassthroughAll
	}
	if patch.HostMounts != nil {
		cfg.HostMounts = *patch.HostMounts
	}
}

func applyPermissionsPatch(cfg *PermissionsConfig, patch *permissionsPatch) {
	if patch.Docker != nil {
		cfg.Docker = *patch.Docker
	}
}
