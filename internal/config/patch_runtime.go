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
	if patch.Agents != nil {
		if dst.Agents == nil {
			dst.Agents = make(map[string]AgentConfig)
		}
		for name, agentPatch := range *patch.Agents {
			current := dst.Agents[name]
			if agentPatch.Model != nil {
				current.Model = *agentPatch.Model
			}
			dst.Agents[name] = current
		}
	}
}

func applyAdvisorPatch(dst *AdvisorConfig, patch *advisorPatch) {
	if patch.Enabled != nil {
		dst.Enabled = *patch.Enabled
	}
	if patch.Model != nil {
		dst.Model = *patch.Model
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
	if patch.Models != nil {
		if dst.Models == nil {
			dst.Models = make(map[string]string)
		}
		for phase, alias := range *patch.Models {
			dst.Models[phase] = alias
		}
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
