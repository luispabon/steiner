package config

import (
	"fmt"
	"strings"
)

func validate(cfg Config) error {
	var problems []string

	validateDefaultModel(&problems, cfg)
	validateProvidersConfig(&problems, cfg.Providers)
	validateSchedulerConfig(&problems, cfg.Scheduler)
	validateModelsConfig(&problems, cfg.Models.Definitions, cfg.Providers)
	validateWorkflowHandoffConfig(&problems, cfg.Models.WorkflowHandoff, cfg.Models.Definitions)
	validateOneShotConfig(&problems, cfg.Models.OneShot, cfg.Models.Definitions)
	validateLimitsConfig(&problems, cfg.Limits)
	validateSubAgentConfig(&problems, cfg.SubAgent, cfg.Models.SubAgents)
	validateAdvisorConfig(&problems, cfg.Advisor, cfg.Models.Advisor)
	validateProjectContextConfig(&problems, cfg.ProjectContext)
	validateLoggingConfig(&problems, cfg.Logging)
	validateToolsConfig(&problems, cfg.Tools)
	validateSearchConfig(&problems, cfg.Search)
	validateDesktopNotificationsConfig(&problems, cfg.DesktopNotifications)
	validateModesConfig(&problems, cfg.Modes)

	if len(problems) > 0 {
		return fmt.Errorf("invalid config: %s", strings.Join(problems, "; "))
	}
	return nil
}

func validateDefaultModel(problems *[]string, cfg Config) {
	if cfg.Models.Default == "" {
		*problems = append(*problems, "models.default is required")
		return
	}
	if _, ok := cfg.Models.Definitions[cfg.Models.Default]; !ok {
		*problems = append(*problems, fmt.Sprintf("models.default %q is not defined in models.definitions", cfg.Models.Default))
	}
}

var validWorkflowHandoffDestinations = map[string]bool{
	"implement": true,
	"review":    true,
}

func validateWorkflowHandoffConfig(problems *[]string, workflowHandoff map[string]string, models map[string]ModelConfig) {
	for destination, alias := range workflowHandoff {
		if !validWorkflowHandoffDestinations[destination] {
			*problems = append(*problems, fmt.Sprintf("models.workflow_handoff contains unknown destination %q", destination))
			continue
		}
		if _, ok := models[alias]; !ok {
			*problems = append(*problems, fmt.Sprintf("models.workflow_handoff[%q] %q is not defined in models.definitions", destination, alias))
		}
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

func validateDesktopNotificationsConfig(problems *[]string, cfg desktopNotificationsConfig) {
	if cfg.Duration < 0 {
		*problems = append(*problems, "desktop_notifications.duration must be non-negative")
	}
}
