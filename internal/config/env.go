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

		next, ok := expandEnvToken(input, i, lookup, &b)
		if !ok {
			b.WriteByte(input[i])
			i++
			continue
		}
		i = next
	}
	return b.String()
}

func applyEnvOverrides(cfg *Config, env map[string]string) error {
	lookup := func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	}

	if err := applyEnvModelOverride(cfg, lookup); err != nil {
		return err
	}
	if err := applyEnvIntOverride(&cfg.Scheduler.Parallelism, "STEINER_SCHEDULER_PARALLELISM", lookup); err != nil {
		return err
	}
	if err := applyEnvIntOverride(&cfg.Limits.MaxTurns, "STEINER_MAX_TURNS", lookup); err != nil {
		return err
	}
	if err := applyEnvIntOverride(&cfg.Limits.MaxTokens, "STEINER_MAX_TOKENS", lookup); err != nil {
		return err
	}
	if err := applyEnvIntOverride(&cfg.Limits.ToolOutputMaxBytes, "STEINER_TOOL_OUTPUT_MAX_BYTES", lookup); err != nil {
		return err
	}
	if value, ok := lookup("STEINER_LOG_LEVEL"); ok {
		cfg.Logging.Level = value
	}
	if value, ok := lookup("STEINER_LOG_FILE"); ok {
		cfg.Logging.File = value
	}
	return nil
}

func expandEnvToken(input string, i int, lookup func(string) (string, bool), b *strings.Builder) (int, bool) {
	if i+1 < len(input) && input[i+1] == '$' {
		b.WriteByte('$')
		return i + 2, true
	}
	if i+1 < len(input) && input[i+1] == '{' {
		return expandEnvBraceToken(input, i, lookup, b)
	}
	return expandEnvBareToken(input, i, lookup, b)
}

func expandEnvBraceToken(input string, i int, lookup func(string) (string, bool), b *strings.Builder) (int, bool) {
	end := strings.IndexByte(input[i+2:], '}')
	if end < 0 {
		return i, false
	}
	expr := input[i+2 : i+2+end]
	name, defaultValue, hasDefault := strings.Cut(expr, ":-")
	if !isEnvName(name) {
		b.WriteString("${")
		b.WriteString(expr)
		b.WriteByte('}')
		return i + end + 3, true
	}
	if value, ok := lookup(name); ok && value != "" {
		b.WriteString(value)
	} else if hasDefault {
		b.WriteString(expandEnvText(defaultValue, lookup))
	}
	return i + end + 3, true
}

func expandEnvBareToken(input string, i int, lookup func(string) (string, bool), b *strings.Builder) (int, bool) {
	j := i + 1
	for j < len(input) && isEnvContinue(input[j], j == i+1) {
		j++
	}
	if j == i+1 {
		return i, false
	}
	if value, ok := lookup(input[i+1 : j]); ok {
		b.WriteString(value)
	}
	return j, true
}

func applyEnvModelOverride(cfg *Config, lookup func(string) (string, bool)) error {
	value, ok := lookup("STEINER_MODEL")
	if !ok {
		return nil
	}
	if model, ok := cfg.Models[value]; ok {
		cfg.Model = model
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
