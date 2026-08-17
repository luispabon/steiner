package config

import (
	"fmt"
	"sort"
	"strings"
)

// validAgentTypes mirrors delegation.AllAgentTypes to avoid a circular import
// (internal/delegation imports internal/config).
var validAgentTypes = map[string]bool{
	"explore":      true,
	"research":     true,
	"code":         true,
	"evaluate":     true,
	"sanity_check": true,
	"review":       true,
	"vision":       true,
}

var validOneShotPhases = map[string]bool{
	"plan":      true,
	"implement": true,
	"review":    true,
}

func validateSubAgentConfig(problems *[]string, cfg SubAgentConfig, subAgents map[string]string, models map[string]ModelConfig) {
	validateModelAliasMap(problems, "models.sub_agents", "agent type", subAgents, validAgentTypes, models)
	if cfg.MaxParallel < 0 {
		*problems = append(*problems, "sub_agent.max_parallel must not be negative")
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
		if cfg.Timeout != nil && cfg.Timeout.IsZero() {
			*problems = append(*problems, "advisor.timeout must be greater than zero when set")
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
	if cfg.Timeout != nil && cfg.Timeout.IsZero() {
		*problems = append(*problems, "advisor.timeout must be greater than zero when set")
	}
}

func validateProjectContextConfig(problems *[]string, cfg ProjectContextConfig) {
	if cfg.MaxBytes < 1 {
		*problems = append(*problems, "project_context.max_bytes must be at least 1")
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

// reservedToolNames lists built-in tool names that must not be overridden
// by user config tools. Source of truth: internal/tool/builtin/builtins.go
// (the ToolDef.Name values returned by Builtins). This set is declared here
// to avoid an import cycle (internal/tool imports internal/config).
var reservedToolNames = map[string]bool{
	"read":             true,
	"glob":             true,
	"grep":             true,
	"ls":               true,
	"bash":             true,
	"display_file":     true,
	"mutate":           true,
	"fetch_url":        true,
	"workflow_handoff": true,
}

// ReservedToolNames returns a sorted copy of the built-in tool names that
// config tools may not override. The set mirrors internal/tool/builtin/builtins.go
// and is declared here to avoid an import cycle.
func ReservedToolNames() []string {
	names := make([]string, 0, len(reservedToolNames))
	for name := range reservedToolNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validateToolsConfig(problems *[]string, tools map[string]ToolConfig) {
	for name, tool := range tools {
		if reservedToolNames[name] {
			*problems = append(*problems, fmt.Sprintf("tools[%q]: name is reserved by a built-in tool", name))
		}
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
	validateModelAliasMap(problems, "models.oneshot", "phase", oneShot, validOneShotPhases, models)
}

func validateSandboxConfig(problems *[]string, cfg SandboxConfig) {
	for i, entry := range cfg.EnvPassthrough {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			*problems = append(*problems, fmt.Sprintf("sandbox.env_passthrough[%d] (%q): must not be empty", i, entry))
			continue
		}
		if trimmed == "*" {
			*problems = append(*problems, fmt.Sprintf("sandbox.env_passthrough[%d] (%q): a bare '*' would pass through the entire environment; use sandbox.env_passthrough_all instead", i, entry))
			continue
		}
		if strings.Contains(trimmed, "=") {
			*problems = append(*problems, fmt.Sprintf("sandbox.env_passthrough[%d] (%q): must not contain '='", i, entry))
			continue
		}
		if strings.Contains(strings.TrimSuffix(trimmed, "*"), "*") {
			*problems = append(*problems, fmt.Sprintf("sandbox.env_passthrough[%d] (%q): '*' is only permitted as the final character", i, entry))
		}
	}
}
