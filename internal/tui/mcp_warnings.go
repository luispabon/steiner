package tui

import (
	"fmt"
	"strings"
)

// mcpFailureState reports whether state is a terminal connection failure that
// warrants a user-facing warning: the initial connect failed, or reconnect
// attempts were exhausted. In-flight states (connecting, reconnecting) and
// healthy states (connected, disabled) are not failures.
func mcpFailureState(state string) bool {
	return state == "failed" || state == "unavailable"
}

// mcpStartupWarnings returns the transcript lines to append at startup for
// any MCP server that failed to connect: one line per failure, plus a single
// aggregate line naming all failures. Returns nil when MCP is disabled or
// every server connected — a healthy or disabled startup stays silent.
func mcpStartupWarnings(servers []MCPServerStatus, enabled bool) []string {
	if !enabled {
		return nil
	}

	var lines []string
	var failed []string
	for _, s := range servers {
		if !mcpFailureState(s.State) {
			continue
		}
		errText := s.Error
		if errText == "" {
			errText = "unknown error"
		}
		lines = append(lines, fmt.Sprintf("⚠ MCP server %q failed to connect: %s", s.Name, errText))
		failed = append(failed, s.Name)
	}
	if len(failed) == 0 {
		return nil
	}

	lines = append(lines, fmt.Sprintf("⚠ MCP startup incomplete (failed: %s)", strings.Join(failed, ", ")))
	return lines
}

// mcpTransitionWarnings returns transcript lines for servers that newly entered
// a failure state since the last status event, plus the updated warned set.
// Each server warns at most once per failure generation: the flag clears when
// the server transitions to connected, so a failure after recovery warns again.
// In-flight states never warn and disabled servers are skipped.
func mcpTransitionWarnings(servers []MCPServerStatus, warned map[string]bool) ([]string, map[string]bool) {
	if warned == nil {
		warned = make(map[string]bool)
	}
	var lines []string
	for _, s := range servers {
		if s.State == "connected" {
			delete(warned, s.Name)
			continue
		}
		if !mcpFailureState(s.State) || warned[s.Name] {
			continue
		}
		warned[s.Name] = true
		errText := s.Error
		if errText == "" {
			errText = "unknown error"
		}
		lines = append(lines, fmt.Sprintf("⚠ MCP server %q failed to connect: %s", s.Name, errText))
	}
	return lines, warned
}
