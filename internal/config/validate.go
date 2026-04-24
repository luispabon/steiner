package config

import (
	"fmt"
	"strings"
)

func Validate(cfg Config) error {
	var problems []string

	if strings.TrimSpace(cfg.Model) == "" {
		problems = append(problems, "model is required")
	}
	if cfg.Scheduler.Parallelism < 1 {
		problems = append(problems, "scheduler.parallelism must be at least 1")
	}
	if len(cfg.Models) == 0 {
		problems = append(problems, "models is required")
	}
	selected, ok := cfg.Models[strings.TrimSpace(cfg.Model)]
	if !ok {
		problems = append(problems, fmt.Sprintf("model %q is not defined", cfg.Model))
	}
	for name, model := range cfg.Models {
		if strings.TrimSpace(name) == "" {
			problems = append(problems, "models contains an empty alias")
		}
		if model.Type == "" {
			problems = append(problems, fmt.Sprintf("models[%q].type is required", name))
		} else if model.Type != "openai_compat" {
			problems = append(problems, fmt.Sprintf("models[%q].type %q is not supported", name, model.Type))
		}
		if strings.TrimSpace(model.BaseURL) == "" {
			problems = append(problems, fmt.Sprintf("models[%q].base_url is required", name))
		}
		if strings.TrimSpace(model.Model) == "" {
			problems = append(problems, fmt.Sprintf("models[%q].model is required", name))
		}
		if model.Temperature != nil && (*model.Temperature < 0 || *model.Temperature > 2) {
			problems = append(problems, fmt.Sprintf("models[%q].temperature must be between 0 and 2", name))
		}
		if model.MaxCompletionTokens < 1 {
			problems = append(problems, fmt.Sprintf("models[%q].max_completion_tokens must be at least 1", name))
		}
		if model.ContextSize < 1 {
			problems = append(problems, fmt.Sprintf("models[%q].context_size must be at least 1", name))
		}
		if model.Compaction.SafetyMarginTokens < 0 {
			problems = append(problems, fmt.Sprintf("models[%q].compaction.safety_margin_tokens must be at least 0", name))
		}
		if model.Compaction.SummaryMaxTokens < 1 {
			problems = append(problems, fmt.Sprintf("models[%q].compaction.summary_max_tokens must be at least 1", name))
		}
		if model.Compaction.SummaryMaxTokens > model.MaxCompletionTokens {
			problems = append(problems, fmt.Sprintf("models[%q].compaction.summary_max_tokens must be less than or equal to models[%q].max_completion_tokens", name, name))
		}
	}
	if ok {
		if selected.Type == "" {
			problems = append(problems, fmt.Sprintf("model %q has an empty type", cfg.Model))
		}
	}
	if cfg.Limits.MaxTurns < 0 {
		problems = append(problems, "limits.max_turns must be non-negative")
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
