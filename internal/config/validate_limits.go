package config

import "fmt"

func validateLimitsConfig(problems *[]string, cfg LimitsConfig) {
	if cfg.MaxTurns < 0 {
		*problems = append(*problems, "limits.max_turns must be non-negative")
	}
	if cfg.MaxTokens < 1 {
		*problems = append(*problems, "limits.max_tokens must be at least 1")
	}
	if cfg.ToolTimeoutDefault.IsZero() {
		*problems = append(*problems, "limits.tool_timeout_default must be greater than zero")
	}
	if cfg.ToolOutputMaxBytes < 1 {
		*problems = append(*problems, "limits.tool_output_max_bytes must be at least 1")
	}
	for name, timeout := range cfg.ToolTimeouts {
		if name == "" {
			*problems = append(*problems, "limits.tool_timeouts contains an empty tool name")
			continue
		}
		if timeout.IsZero() {
			*problems = append(*problems, fmt.Sprintf("limits.tool_timeouts[%q] must be greater than zero", name))
		}
	}
}
