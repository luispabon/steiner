package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func expandEnvText(input string, lookup func(string) (string, bool)) string {
	if input == "" {
		return input
	}

	var b strings.Builder
	for i := 0; i < len(input); {
		if input[i] != '$' {
			b.WriteByte(input[i])
			i++
			continue
		}

		if i+1 < len(input) && input[i+1] == '$' {
			b.WriteByte('$')
			i += 2
			continue
		}

		if i+1 < len(input) && input[i+1] == '{' {
			end := strings.IndexByte(input[i+2:], '}')
			if end < 0 {
				b.WriteByte(input[i])
				i++
				continue
			}
			expr := input[i+2 : i+2+end]
			name, defaultValue, hasDefault := strings.Cut(expr, ":-")
			if !isEnvName(name) {
				b.WriteString("${")
				b.WriteString(expr)
				b.WriteByte('}')
				i += end + 3
				continue
			}
			if value, ok := lookup(name); ok && value != "" {
				b.WriteString(value)
			} else if hasDefault {
				b.WriteString(expandEnvText(defaultValue, lookup))
			}
			i += end + 3
			continue
		}

		j := i + 1
		for j < len(input) && isEnvContinue(input[j], j == i+1) {
			j++
		}
		if j == i+1 {
			b.WriteByte(input[i])
			i++
			continue
		}
		name := input[i+1 : j]
		if value, ok := lookup(name); ok {
			b.WriteString(value)
		}
		i = j
	}
	return b.String()
}

func applyEnvOverrides(cfg *Config, env map[string]string) error {
	lookup := func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	}

	if value, ok := lookup("STEINER_MODEL"); ok {
		if m, ok := cfg.Models[value]; ok {
			cfg.Model = m
		}
	}
	if value, ok := lookup("STEINER_SCHEDULER_PARALLELISM"); ok {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid STEINER_SCHEDULER_PARALLELISM: %w", err)
		}
		cfg.Scheduler.Parallelism = parsed
	}
	if value, ok := lookup("STEINER_MAX_TURNS"); ok {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid STEINER_MAX_TURNS: %w", err)
		}
		cfg.Limits.MaxTurns = parsed
	}
	if value, ok := lookup("STEINER_MAX_TOKENS"); ok {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid STEINER_MAX_TOKENS: %w", err)
		}
		cfg.Limits.MaxTokens = parsed
	}
	if value, ok := lookup("STEINER_TOOL_OUTPUT_MAX_BYTES"); ok {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid STEINER_TOOL_OUTPUT_MAX_BYTES: %w", err)
		}
		cfg.Limits.ToolOutputMaxBytes = parsed
	}
	if value, ok := lookup("STEINER_LOG_LEVEL"); ok {
		cfg.Logging.Level = value
	}
	if value, ok := lookup("STEINER_LOG_FILE"); ok {
		cfg.Logging.File = value
	}
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
		return ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
	}
	return ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
}

func parseDurationString(value string) (time.Duration, error) {
	return time.ParseDuration(strings.TrimSpace(value))
}

func formatDurationNanos(nanos int64) string {
	return time.Duration(nanos).String()
}
