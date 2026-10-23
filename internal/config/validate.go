package config

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// validate checks cfg for problems. projectRoot is the resolved working
// directory; it may be empty, which skips checks that need it.
func validate(cfg Config, projectRoot string) error {
	var problems []string

	validateProfilesConfig(&problems, cfg)
	validateProvidersConfig(&problems, cfg.Providers)
	validateTUIConfig(&problems, cfg.TUI)
	validateModelsConfig(&problems, cfg.Models.Definitions, cfg.Providers)
	validateLimitsConfig(&problems, cfg.Limits)
	validateProjectContextConfig(&problems, cfg.ProjectContext)
	validateLoggingConfig(&problems, cfg.Logging)
	validateDiagnosticsConfig(&problems, cfg.Diagnostics, projectRoot)
	validateToolsConfig(&problems, cfg.Tools)
	validateSandboxConfig(&problems, cfg.Sandbox)
	validateSearchConfig(&problems, cfg.Search)
	validateMCPConfig(&problems, cfg.MCP)
	if err := validateLSP(cfg.LSP); err != nil {
		problems = append(problems, err.Error())
	}
	validateDesktopNotificationsConfig(&problems, cfg.DesktopNotifications)
	validateUpdateCheckConfig(&problems, cfg.UpdateCheck)
	validateModesConfig(&problems, cfg.Modes)

	if len(problems) > 0 {
		return fmt.Errorf("invalid config: %s", strings.Join(problems, "; "))
	}
	return nil
}

func validateProfilesConfig(problems *[]string, cfg Config) {
	names := make([]string, 0, len(cfg.Models.Profiles))
	for name := range cfg.Models.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.TrimSpace(cfg.Models.Profiles[name].DefaultModel) == "" {
			*problems = append(*problems, fmt.Sprintf("models.profiles.%s.default_model is required", name))
			continue
		}
		if _, err := ResolveEffectiveAssignments(&cfg, name); err != nil {
			*problems = append(*problems, err.Error())
		}
	}

	validateSubAgentConfig(problems, cfg.SubAgent)
	validateAdvisorConfig(problems, cfg.Advisor)
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

// maxBashTimeoutCapSeconds is the largest accepted limits.tool_timeouts.bash
// value. It mirrors maxBashTimeoutCapSeconds in internal/tool/builtin, which
// this package cannot import because internal/tool imports internal/config.
const maxBashTimeoutCapSeconds = 600

func validateLimitsConfig(problems *[]string, cfg LimitsConfig) {
	if cfg.MaxTurns < 0 {
		*problems = append(*problems, "limits.max_turns must be non-negative")
	}
	if cfg.MaxTokens < 1 {
		*problems = append(*problems, "limits.max_tokens must be at least 1")
	}
	if cfg.ToolTimeoutDefault.Duration() <= 0 {
		*problems = append(*problems, "limits.tool_timeout_default must be greater than zero")
	}
	if cfg.ToolOutputMaxBytes < 1 {
		*problems = append(*problems, "limits.tool_output_max_bytes must be at least 1")
	}
	if cfg.MaxParallelTools == 0 {
		*problems = append(*problems, "limits.max_parallel_tools must be at least 1 (0 previously meant unbounded; use 1 for the equivalent serial behaviour, or a higher value for bounded concurrency)")
	} else if cfg.MaxParallelTools < 0 {
		*problems = append(*problems, "limits.max_parallel_tools must be at least 1")
	}
	for name, timeout := range cfg.ToolTimeouts {
		if name == "" {
			*problems = append(*problems, "limits.tool_timeouts contains an empty tool name")
			continue
		}
		if timeout.Duration() <= 0 {
			*problems = append(*problems, fmt.Sprintf("limits.tool_timeouts[%q] must be greater than zero", name))
			continue
		}
		// The Bash cap must be a whole number of seconds; other tools may keep
		// sub-second timeouts.
		if name == "bash" && timeout.Duration()%int64(time.Second) != 0 {
			*problems = append(*problems, fmt.Sprintf("limits.tool_timeouts[%q] must be a whole number of seconds", name))
		}
		if name == "bash" && timeout.Duration() > maxBashTimeoutCapSeconds*int64(time.Second) {
			*problems = append(*problems, fmt.Sprintf("limits.tool_timeouts[%q] must be at most %d seconds", name, maxBashTimeoutCapSeconds))
		}
	}
}

func validateDesktopNotificationsConfig(problems *[]string, cfg desktopNotificationsConfig) {
	if cfg.Duration < 0 {
		*problems = append(*problems, "desktop_notifications.duration must be non-negative")
	}
}

func validateUpdateCheckConfig(problems *[]string, cfg UpdateCheckConfig) {
	if cfg.IntervalHours < 0 {
		*problems = append(*problems, "update_check.interval_hours must be non-negative")
	}
}
