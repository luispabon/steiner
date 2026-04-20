package config

import (
	"fmt"
	"strings"
)

func Validate(cfg Config) error {
	var problems []string

	if cfg.Provider.Type == "" {
		problems = append(problems, "provider.type is required")
	} else if cfg.Provider.Type != "openai_compat" {
		problems = append(problems, fmt.Sprintf("provider.type %q is not supported", cfg.Provider.Type))
	}
	if strings.TrimSpace(cfg.Provider.BaseURL) == "" {
		problems = append(problems, "provider.base_url is required")
	}
	if strings.TrimSpace(cfg.Provider.Model) == "" {
		problems = append(problems, "provider.model is required")
	}
	if cfg.Provider.Parallelism < 1 {
		problems = append(problems, "provider.parallelism must be at least 1")
	}
	if cfg.Provider.Temperature < 0 || cfg.Provider.Temperature > 2 {
		problems = append(problems, "provider.temperature must be between 0 and 2")
	}
	if cfg.Provider.MaxCompletionTokens < 1 {
		problems = append(problems, "provider.max_completion_tokens must be at least 1")
	}
	if cfg.Limits.MaxTurns < 1 {
		problems = append(problems, "limits.max_turns must be at least 1")
	}
	if cfg.Limits.MaxTokens < 1 {
		problems = append(problems, "limits.max_tokens must be at least 1")
	}
	if cfg.Limits.ToolTimeoutDefault.IsZero() {
		problems = append(problems, "limits.tool_timeout_default must be greater than zero")
	}
	if cfg.Limits.ToolOutputMaxBytes < 1 {
		problems = append(problems, "limits.tool_output_max_bytes must be at least 1")
	}
	for name, timeout := range cfg.Limits.ToolTimeouts {
		if name == "" {
			problems = append(problems, "limits.tool_timeouts contains an empty tool name")
			continue
		}
		if timeout.IsZero() {
			problems = append(problems, fmt.Sprintf("limits.tool_timeouts[%q] must be greater than zero", name))
		}
	}
	if err := validateApprovalMode("approval.default", cfg.Approval.Default); err != nil {
		problems = append(problems, err.Error())
	}
	for name, mode := range cfg.Approval.Overrides {
		if err := validateApprovalMode(fmt.Sprintf("approval.overrides[%q]", name), mode); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if cfg.SubAgent.Enabled {
		if cfg.SubAgent.MaxTurns < 1 {
			problems = append(problems, "sub_agent.max_turns must be at least 1 when enabled")
		}
		if cfg.SubAgent.MaxTokens < 1 {
			problems = append(problems, "sub_agent.max_tokens must be at least 1 when enabled")
		}
		if cfg.SubAgent.MaxConcurrent < 1 {
			problems = append(problems, "sub_agent.max_concurrent must be at least 1 when enabled")
		}
	}
	if cfg.ProjectContext.MaxTokens < 1 {
		problems = append(problems, "project_context.max_tokens must be at least 1")
	}
	if err := validateLoggingLevel(cfg.Logging.Level); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(cfg.Logging.File) == "" {
		problems = append(problems, "logging.file is required")
	}
	for name, tool := range cfg.Tools {
		if strings.TrimSpace(name) == "" {
			problems = append(problems, "tools contains an empty tool name")
		}
		if strings.TrimSpace(tool.Exec) == "" {
			problems = append(problems, fmt.Sprintf("tools[%q].exec is required", name))
		}
		if tool.Timeout.IsZero() {
			problems = append(problems, fmt.Sprintf("tools[%q].timeout must be greater than zero", name))
		}
		if err := validateApprovalMode(fmt.Sprintf("tools[%q].approval", name), tool.Approval); err != nil {
			problems = append(problems, err.Error())
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid config: %s", strings.Join(problems, "; "))
	}
	return nil
}

func validateApprovalMode(path string, mode ApprovalMode) error {
	switch mode {
	case ApprovalModeAuto, ApprovalModePrompt, ApprovalModeDeny:
		return nil
	case "":
		return fmt.Errorf("%s is required", path)
	default:
		return fmt.Errorf("%s %q is not supported", path, mode)
	}
}

func validateLoggingLevel(level string) error {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace", "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("logging.level %q is not supported", level)
	}
}
