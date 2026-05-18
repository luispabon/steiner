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
}

func validateSubAgentConfig(problems *[]string, cfg SubAgentConfig) {
	for name := range cfg.Agents {
		if !validAgentTypes[name] {
			*problems = append(*problems, fmt.Sprintf("sub_agent.agents contains unknown agent type %q", name))
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
		if tool.Approval != "" {
			if err := validateApprovalMode(fmt.Sprintf("tools[%q].approval", name), tool.Approval); err != nil {
				*problems = append(*problems, err.Error())
			}
		}
	}
}

func validateContextManagementConfig(problems *[]string, cfg ContextManagementConfig) {
	if err := validateContextMode("context_management.mode", cfg.Mode); err != nil {
		*problems = append(*problems, err.Error())
	}
	if cfg.CompactionStrategy != "" {
		if err := validateCompactionStrategy("context_management.compaction_strategy", cfg.CompactionStrategy); err != nil {
			*problems = append(*problems, err.Error())
		}
	}
	if err := validateScratchpadMode("context_management.scratchpad_mode", cfg.ScratchpadMode); err != nil {
		*problems = append(*problems, err.Error())
	}
	if cfg.MaskingWindowTurns <= 0 {
		*problems = append(*problems, "context_management.masking_window_turns must be at least 1")
	}
}
