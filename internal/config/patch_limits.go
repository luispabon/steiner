package config

func applyLimitsPatch(dst *LimitsConfig, patch *limitsPatch) {
	if patch.MaxTurns != nil {
		dst.MaxTurns = *patch.MaxTurns
	}
	if patch.MaxTokens != nil {
		dst.MaxTokens = *patch.MaxTokens
	}
	if patch.ToolTimeoutDefault != nil {
		dst.ToolTimeoutDefault = *patch.ToolTimeoutDefault
	}
	if patch.ToolTimeouts != nil {
		if dst.ToolTimeouts == nil {
			dst.ToolTimeouts = make(map[string]Duration)
		}
		for name, timeout := range *patch.ToolTimeouts {
			dst.ToolTimeouts[name] = timeout
		}
	}
	if patch.ToolOutputMaxBytes != nil {
		dst.ToolOutputMaxBytes = *patch.ToolOutputMaxBytes
	}
}
