package tui

import (
	"fmt"
	"strings"
)

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
		if s.State != "failed" {
			continue
		}
		lines = append(lines, fmt.Sprintf("⚠ MCP server %q failed to connect: %s", s.Name, s.Error))
		failed = append(failed, s.Name)
	}
	if len(failed) == 0 {
		return nil
	}

	lines = append(lines, fmt.Sprintf("⚠ MCP startup incomplete (failed: %s)", strings.Join(failed, ", ")))
	return lines
}
