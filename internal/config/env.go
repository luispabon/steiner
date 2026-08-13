package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func applyEnvOverrides(cfg *Config, env map[string]string) error {
	lookup := func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	}

	if err := applyEnvModelOverride(cfg, lookup); err != nil {
		return err
	}

	if err := applyEnvIntOverrides(cfg, lookup); err != nil {
		return err
	}
	applyEnvLoggingOverrides(cfg, lookup)
	applyEnvSearchOverrides(cfg, lookup)

	return nil
}

func applyEnvIntOverrides(cfg *Config, lookup func(string) (string, bool)) error {
	overrides := []struct {
		target *int
		name   string
	}{
		{&cfg.Scheduler.Parallelism, "STEINER_SCHEDULER_PARALLELISM"},
		{&cfg.TUI.FPS, "STEINER_TUI_FPS"},
		{&cfg.Limits.MaxTurns, "STEINER_MAX_TURNS"},
		{&cfg.Limits.MaxTokens, "STEINER_MAX_TOKENS"},
		{&cfg.Limits.ToolOutputMaxBytes, "STEINER_TOOL_OUTPUT_MAX_BYTES"},
	}
	for _, o := range overrides {
		if err := applyEnvIntOverride(o.target, o.name, lookup); err != nil {
			return err
		}
	}
	return nil
}

func applyEnvLoggingOverrides(cfg *Config, lookup func(string) (string, bool)) {
	if value, ok := lookup("STEINER_LOG_LEVEL"); ok {
		cfg.Logging.Level = value
	}
	if value, ok := lookup("STEINER_LOG_FILE"); ok {
		cfg.Logging.File = value
	}
	if value, ok := lookup("STEINER_COMPACTION_LOG_FILE"); ok {
		cfg.Logging.CompactionLogFile = value
	}
}

func applyEnvSearchOverrides(cfg *Config, lookup func(string) (string, bool)) {
	overrides := []struct {
		dst *string
		env string
	}{
		{&cfg.Search.GoogleCx, "GOOGLE_SEARCH_CX"},
		{&cfg.Search.GoogleAPIKey, "GOOGLE_SEARCH_API_KEY"},
		{&cfg.Search.KagiAPIKey, "KAGI_API_KEY"},
		{&cfg.Search.BraveAPIKey, "BRAVE_API_KEY"},
	}
	for _, o := range overrides {
		if *o.dst == "" {
			if v, ok := lookup(o.env); ok {
				*o.dst = v
			}
		}
	}
}

func applyEnvModelOverride(cfg *Config, lookup func(string) (string, bool)) error {
	value, ok := lookup("STEINER_MODEL")
	if !ok {
		return nil
	}
	if _, ok := cfg.Models.Definitions[value]; ok {
		cfg.Models.Default = value
	}
	return nil
}

func applyEnvIntOverride(target *int, name string, lookup func(string) (string, bool)) error {
	value, ok := lookup(name)
	if !ok {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", name, err)
	}
	*target = parsed
	return nil
}

func isEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if i == 0 {
			if !isEnvStart(name[i]) {
				return false
			}
			continue
		}
		if !isEnvContinue(name[i], false) {
			return false
		}
	}
	return true
}

func isEnvStart(ch byte) bool {
	return ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
}

func isEnvContinue(ch byte, first bool) bool {
	if first {
		return isEnvStart(ch)
	}
	return isEnvStart(ch) || (ch >= '0' && ch <= '9')
}

func parseDurationString(value string) (time.Duration, error) {
	return time.ParseDuration(strings.TrimSpace(value))
}

func formatDurationNanos(nanos int64) string {
	return time.Duration(nanos).String()
}
