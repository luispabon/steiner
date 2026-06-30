package config

import (
	"fmt"
	"strings"
)

// validAgentTypes mirrors delegation.AllAgentTypes to avoid a circular import
// (internal/delegation imports internal/config).
var validAgentTypes = map[string]bool{
	"explore":  true,
	"research": true,
	"code":     true,
	"plan":     true,
	"verify":   true,
	"vision":   true,
}

var validOneShotPhases = map[string]bool{
	"plan":      true,
	"implement": true,
	"review":    true,
}

func validateSubAgentConfig(problems *[]string, cfg SubAgentConfig, subAgents map[string]string) {
	for name := range subAgents {
		if !validAgentTypes[name] {
			*problems = append(*problems, fmt.Sprintf("models.sub_agents contains unknown agent type %q", name))
		}
	}
	if !cfg.Enabled {
		return
	}
	if cfg.MaxTurns < 1 {
		*problems = append(*problems, "sub_agent.max_turns must be at least 1 when enabled")
	}
	if cfg.MaxTokens < 1 {
		*problems = append(*problems, "sub_agent.max_tokens must be at least 1 when enabled")
	}
}

func validateAdvisorConfig(problems *[]string, cfg AdvisorConfig, model string) {
	if !cfg.Enabled {
		if cfg.MaxTokens != nil && *cfg.MaxTokens < 1 {
			*problems = append(*problems, "advisor.max_tokens must be greater than zero when set")
		}
		return
	}
	if strings.TrimSpace(model) == "" {
		*problems = append(*problems, "models.advisor is required when enabled")
	}
	if cfg.MaxUsesPerRun < 1 {
		*problems = append(*problems, "advisor.max_uses_per_run must be at least 1 when enabled")
	}
	if cfg.MaxTokens != nil && *cfg.MaxTokens < 1 {
		*problems = append(*problems, "advisor.max_tokens must be greater than zero when set")
	}
}

func validateProjectContextConfig(problems *[]string, cfg ProjectContextConfig) {
	if cfg.MaxTokens < 1 {
		*problems = append(*problems, "project_context.max_tokens must be at least 1")
	}
}

func validateLoggingConfig(problems *[]string, cfg LoggingConfig) {
	if err := validateLoggingLevel(cfg.Level); err != nil {
		*problems = append(*problems, err.Error())
	}
	if strings.TrimSpace(cfg.File) == "" {
		*problems = append(*problems, "logging.file is required")
	}
}

func validateToolsConfig(problems *[]string, tools map[string]ToolConfig) {
	for name, tool := range tools {
		if strings.TrimSpace(name) == "" {
			*problems = append(*problems, "tools contains an empty tool name")
		}
		if strings.TrimSpace(tool.Exec) == "" {
			*problems = append(*problems, fmt.Sprintf("tools[%q].exec is required", name))
		}
		if tool.Timeout.IsZero() {
			*problems = append(*problems, fmt.Sprintf("tools[%q].timeout must be greater than zero", name))
		}
	}
}

func validateOneShotConfig(problems *[]string, oneShot map[string]string, models map[string]ModelConfig) {
	for phase, alias := range oneShot {
		if !validOneShotPhases[phase] {
			*problems = append(*problems, fmt.Sprintf("models.oneshot contains unknown phase %q", phase))
			continue
		}
		if _, ok := models[alias]; !ok {
			*problems = append(*problems, fmt.Sprintf("models.oneshot[%q] %q is not defined in models.definitions", phase, alias))
		}
	}
}
