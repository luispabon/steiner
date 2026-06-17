package config

func applySubAgentConfigPatch(cfg *Config, patch configPatch) {
	if patch.SubAgent != nil {
		applySubAgentPatch(&cfg.SubAgent, patch.SubAgent)
	}
}

func applyAdvisorConfigPatch(cfg *Config, patch configPatch) {
	if patch.Advisor != nil {
		applyAdvisorPatch(&cfg.Advisor, patch.Advisor)
	}
}

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

func applyToolConfigPatch(cfg *Config, patch configPatch) {
	if patch.Tools == nil {
		return
	}
	if cfg.Tools == nil {
		cfg.Tools = make(map[string]ToolConfig)
	}
	for name, tool := range *patch.Tools {
		current := cfg.Tools[name]
		applyToolPatch(&current, &tool)
		cfg.Tools[name] = current
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
