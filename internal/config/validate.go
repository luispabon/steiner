package config

import (
	"fmt"
	"sort"
	"strings"
)

func validate(cfg Config) error {
	var problems []string

	validateProfilesConfig(&problems, cfg)
	validateProvidersConfig(&problems, cfg.Providers)
	validateTUIConfig(&problems, cfg.TUI)
	validateModelsConfig(&problems, cfg.Models.Definitions, cfg.Providers)
	validateLimitsConfig(&problems, cfg.Limits)
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

func validateProfilesConfig(problems *[]string, cfg Config) {
	baseline, ok := cfg.Models.Profiles["default"]
	if !ok {
		*problems = append(*problems, "models.profiles.default is required")
		return
	}
	if strings.TrimSpace(baseline.DefaultModel) == "" {
		*problems = append(*problems, "models.profiles.default.default_model is required")
		return
	}

	names := make([]string, 0, len(cfg.Models.Profiles))
	for name := range cfg.Models.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		effective, err := ResolveEffectiveAssignments(&cfg, name)
		if err != nil {
			*problems = append(*problems, err.Error())
			continue
		}
		prefix := fmt.Sprintf("models.profiles[%q]", name)
		validateProfileReferences(problems, prefix, effective, cfg)
	}

	effective, err := ResolveEffectiveAssignments(&cfg, "default")
	if err == nil {
		validateSubAgentConfig(problems, cfg.SubAgent, effective.SubAgents, cfg)
		validateAdvisorConfig(problems, cfg.Advisor)
	}
}

func validateProfileReferences(problems *[]string, prefix string, profile EffectiveModelAssignments, cfg Config) {
	if !IsValidModelReference(&cfg, profile.DefaultModel) {
		*problems = append(*problems, fmt.Sprintf("%s.default_model %q is not defined in models.definitions or providers", prefix, profile.DefaultModel))
	}
	if profile.Advisor != "" && !IsValidModelReference(&cfg, profile.Advisor) {
		*problems = append(*problems, fmt.Sprintf("%s.advisor %q is not defined in models.definitions or providers", prefix, profile.Advisor))
	}
	validateModelReferenceMap(problems, prefix+".sub_agents", "agent type", profile.SubAgents, validAgentTypes, cfg)
	validateModelReferenceMap(problems, prefix+".oneshot", "phase", profile.OneShot, validOneShotPhases, cfg)
	validateModelReferenceMap(problems, prefix+".workflow_handoff", "destination", profile.WorkflowHandoff, validWorkflowHandoffDestinations, cfg)
}

var validWorkflowHandoffDestinations = map[string]bool{
	"implement": true,
	"review":    true,
	"build":     true,
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
