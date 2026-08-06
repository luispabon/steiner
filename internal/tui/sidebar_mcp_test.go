package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestStatusSectionShowsMCPConnectedOverTotal(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentAmber)
	s := sidebarState{mcpConnected: 2, mcpTotal: 3, styles: styles}
	lines := s.statusSection(32)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "MCP") || !strings.Contains(joined, "2/3") {
		t.Errorf("statusSection() = %q, want to contain MCP 2/3", joined)
	}
}

func TestStatusSectionMCPExcludesDisabledFromDenominator(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentAmber)
	// 3 servers configured, 1 disabled -> mcpTotal should already be 2
	// (syncSidebar excludes disabled servers before assigning mcpTotal).
	s := sidebarState{mcpConnected: 2, mcpTotal: 2, styles: styles}
	lines := s.statusSection(32)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "2/2") {
		t.Errorf("statusSection() = %q, want to contain 2/2", joined)
	}
	if strings.Contains(joined, "2/3") {
		t.Errorf("statusSection() = %q, disabled server must not inflate denominator", joined)
	}
}

func TestMCPRowEmptyWhenOffOrUnconfigured(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    sidebarState
	}{
		{"mcp off", sidebarState{mcpTotal: 0, mcpConnected: 0}},
		{"nothing configured", sidebarState{mcpTotal: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.s.mcpRow(32); got != "" {
				t.Errorf("mcpRow() = %q, want empty", got)
			}
		})
	}
}

func TestStatusSectionMCPUsesErrorStyleOnFailure(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentAmber)
	ok := sidebarState{mcpConnected: 3, mcpTotal: 3, mcpFailed: false, styles: styles}
	failed := sidebarState{mcpConnected: 2, mcpTotal: 3, mcpFailed: true, styles: styles}

	okLine := strings.Join(ok.statusSection(32), "\n")
	failedLine := strings.Join(failed.statusSection(32), "\n")

	if okLine == failedLine {
		t.Errorf("expected different styling between ok and failed states")
	}
	if !strings.Contains(failedLine, "2/3") {
		t.Errorf("failed section = %q, want to contain %q", failedLine, "2/3")
	}
}

func TestStatusSectionNilAddsNoBlankLineToSidebar(t *testing.T) {
	t.Parallel()
	s := sidebarState{mcpTotal: 0}
	if got := s.statusSection(32); got != nil {
		t.Errorf("statusSection() = %v, want nil when sandbox/skill/MCP all absent", got)
	}
	lines := s.staticLines(32)
	for i, line := range lines {
		if strings.Contains(line, "MCP") {
			t.Errorf("staticLines()[%d] = %q, should not contain MCP row when unconfigured", i, line)
		}
	}
}
