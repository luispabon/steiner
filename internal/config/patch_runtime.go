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
	if patch.AllowedTools != nil {
		dst.AllowedTools = append([]string(nil), (*patch.AllowedTools)...)
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
