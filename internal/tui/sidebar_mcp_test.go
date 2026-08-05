package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestMCPSectionShowsConnectedOverTotal(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentAmber)
	s := sidebarState{mcpConnected: 2, mcpTotal: 3, styles: styles}
	lines := s.mcpSection(32)
	if len(lines) != 1 {
		t.Fatalf("mcpSection() = %d lines, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "MCP 2/3") {
		t.Errorf("mcpSection() = %q, want to contain %q", lines[0], "MCP 2/3")
	}
}

func TestMCPSectionExcludesDisabledFromDenominator(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentAmber)
	// 3 servers configured, 1 disabled -> mcpTotal should already be 2
	// (syncSidebar excludes disabled servers before assigning mcpTotal).
	s := sidebarState{mcpConnected: 2, mcpTotal: 2, styles: styles}
	lines := s.mcpSection(32)
	if len(lines) != 1 {
		t.Fatalf("mcpSection() = %d lines, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "MCP 2/2") {
		t.Errorf("mcpSection() = %q, want to contain %q", lines[0], "MCP 2/2")
	}
	if strings.Contains(lines[0], "2/3") {
		t.Errorf("mcpSection() = %q, disabled server must not inflate denominator", lines[0])
	}
}

func TestMCPSectionNilWhenOffOrUnconfigured(t *testing.T) {
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
			if got := tc.s.mcpSection(32); got != nil {
				t.Errorf("mcpSection() = %v, want nil", got)
			}
		})
	}
}

func TestMCPSectionUsesErrorStyleOnFailure(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentAmber)
	ok := sidebarState{mcpConnected: 3, mcpTotal: 3, mcpFailed: false, styles: styles}
	failed := sidebarState{mcpConnected: 2, mcpTotal: 3, mcpFailed: true, styles: styles}

	okLine := ok.mcpSection(32)[0]
	failedLine := failed.mcpSection(32)[0]

	if okLine == failedLine {
		t.Errorf("expected different styling between ok and failed states")
	}
	if !strings.Contains(failedLine, "MCP 2/3") {
		t.Errorf("failed line = %q, want to contain %q", failedLine, "MCP 2/3")
	}
}

func TestMCPSectionNilAddsNoBlankLineToSidebar(t *testing.T) {
	t.Parallel()
	s := sidebarState{mcpTotal: 0}
	lines := s.staticLines(32)
	for i, line := range lines {
		if strings.Contains(line, "MCP") {
			t.Errorf("staticLines()[%d] = %q, should not contain MCP row when unconfigured", i, line)
		}
	}
}
