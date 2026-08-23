package config

import (
	"fmt"
	"strings"
)

func validate(cfg Config) error {
	var problems []string

	validateDefaultModel(&problems, cfg)
	validateProvidersConfig(&problems, cfg.Providers)
	validateTUIConfig(&problems, cfg.TUI)
	validateModelsConfig(&problems, cfg.Models.Definitions, cfg.Providers)
	validateWorkflowHandoffConfig(&problems, cfg.Models.WorkflowHandoff, cfg)
	validateOneShotConfig(&problems, cfg.Models.OneShot, cfg)
	validateLimitsConfig(&problems, cfg.Limits)
	validateSubAgentConfig(&problems, cfg.SubAgent, cfg.Models.SubAgents, cfg)
	validateAdvisorConfig(&problems, cfg.Advisor, cfg.Models.Advisor, cfg)
	validateProjectContextConfig(&problems, cfg.ProjectContext)
	validateLoggingConfig(&problems, cfg.Logging)
	validateToolsConfig(&problems, cfg.Tools)
	validateSandboxConfig(&problems, cfg.Sandbox)
	validateSearchConfig(&problems, cfg.Search)
	validateMCPConfig(&problems, cfg.MCP)
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
	if !IsValidModelReference(&cfg, cfg.Models.Default) {
		*problems = append(*problems, fmt.Sprintf("models.default %q is not defined in models.definitions or providers", cfg.Models.Default))
	}
}

var validWorkflowHandoffDestinations = map[string]bool{
	"implement": true,
	"review":    true,
	"build":     true,
}

func validateWorkflowHandoffConfig(problems *[]string, workflowHandoff map[string]string, cfg Config) {
	validateModelReferenceMap(problems, "models.workflow_handoff", "destination", workflowHandoff, validWorkflowHandoffDestinations, cfg)
}

// validateModelReferenceMap checks that every key in aliases is a member of
// validKeys and that every value names a model alias or provider/model-id reference.
func validateModelReferenceMap(problems *[]string, mapName, keyLabel string, aliases map[string]string, validKeys map[string]bool, cfg Config) {
	for key, alias := range aliases {
		if !validKeys[key] {
			*problems = append(*problems, fmt.Sprintf("%s contains unknown %s %q", mapName, keyLabel, key))
			continue
		}
		if alias == "" {
			continue // empty alias is the documented disabled sentinel for optional sub-agents.
		}
		if !IsValidModelReference(&cfg, alias) {
			*problems = append(*problems, fmt.Sprintf("%s[%q] %q is not defined in models.definitions or providers", mapName, key, alias))
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
