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
			spinner, count := tc.s.mcpRow()
			if spinner != "" || count != "" {
				t.Errorf("mcpRow() = (%q, %q), want (\"\", \"\")", spinner, count)
			}
		})
	}
}

func TestMCPRowReturnsBareValue(t *testing.T) {
	t.Parallel()
	s := sidebarState{mcpConnected: 2, mcpTotal: 3}
	spinner, count := s.mcpRow()
	if count != "2/3" {
		t.Errorf("mcpRow() count = %q, want %q", count, "2/3")
	}
	if spinner != "" {
		t.Errorf("mcpRow() spinner = %q when not connecting, want empty", spinner)
	}
	if strings.Contains(spinner, "\x1b[") || strings.Contains(count, "\x1b[") {
		t.Errorf("mcpRow() must not contain ANSI escapes; styling is the caller's job (spinner=%q, count=%q)", spinner, count)
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

func TestMCPRowSpinnerWhenConnecting(t *testing.T) {
	cases := []struct {
		name        string
		state       string
		wantSpinner bool
		wantCount   string
	}{
		{"connecting", "connecting", true, "0/1"},
		{"reconnecting", "reconnecting", true, "0/1"},
		{"connected", "connected", false, "1/1"},
		{"failed", "failed", false, "0/1"},
		{"disabled", "disabled", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(Config{}, nil)
			m.mcpServers = []MCPServerStatus{
				{Name: "server1", State: tc.state},
			}
			m.syncSidebar()

			spinner, count := m.sidebar.mcpRow()
			if tc.wantSpinner && spinner == "" {
				t.Errorf("mcpRow() spinner = %q when %s, want non-empty", spinner, tc.state)
			}
			if !tc.wantSpinner && spinner != "" {
				t.Errorf("mcpRow() spinner = %q when %s, want empty", spinner, tc.state)
			}
			if count != tc.wantCount {
				t.Errorf("mcpRow() count = %q when %s, want %q", count, tc.state, tc.wantCount)
			}
		})
	}
}

func TestMCPRowSpinnerFrameAdvances(t *testing.T) {
	t.Parallel()
	s := sidebarState{mcpConnected: 1, mcpTotal: 1, mcpConnecting: true}
	var frames []string
	for tick := 0; tick < len(spinnerFrames)*2; tick++ {
		s.tickCount = tick
		spinner, _ := s.mcpRow()
		frames = append(frames, spinner)
	}
	// First 10 frames should cycle through spinnerFrames
	for i := 0; i < len(spinnerFrames); i++ {
		if frames[i] != spinnerFrames[i] {
			t.Errorf("frame[%d] = %q, want %q", i, frames[i], spinnerFrames[i])
		}
	}
	// Next 10 frames should repeat
	for i := 0; i < len(spinnerFrames); i++ {
		if frames[len(spinnerFrames)+i] != spinnerFrames[i] {
			t.Errorf("frame[%d] (repeat) = %q, want %q", len(spinnerFrames)+i, frames[len(spinnerFrames)+i], spinnerFrames[i])
		}
	}
}

func TestMCPRowEmptyCountWhenZero(t *testing.T) {
	t.Parallel()
	s := sidebarState{mcpConnected: 0, mcpTotal: 0, mcpConnecting: true}
	spinner, count := s.mcpRow()
	if spinner != "" || count != "" {
		t.Errorf("mcpRow() = (%q, %q), want (\"\", \"\") when mcpTotal is 0", spinner, count)
	}
}

func TestStatusSectionMCPBackgroundPreserved(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentAmber)

	// Non-connecting: single cardFieldAccentN call
	s := sidebarState{
		mcpConnected: 1, mcpTotal: 1, mcpConnecting: false,
		tickCount: 0, styles: styles,
	}
	notConnectingLine := strings.Join(s.statusSection(32), "\n")

	// Connecting: composed row (spinner + space + count with separate styling)
	s.mcpConnecting = true
	connectingLine := strings.Join(s.statusSection(32), "\n")

	// Both should render successfully and contain the count
	if !strings.Contains(notConnectingLine, "1/1") {
		t.Errorf("not-connecting statusSection = %q, want to contain 1/1", notConnectingLine)
	}
	if !strings.Contains(connectingLine, "1/1") {
		t.Errorf("connecting statusSection = %q, want to contain 1/1", connectingLine)
	}

	// Spinner should appear only when connecting
	if strings.Contains(notConnectingLine, "⠋") {
		t.Errorf("not-connecting statusSection should not contain spinner frame")
	}
	if !strings.Contains(connectingLine, "⠋") {
		t.Errorf("connecting statusSection should contain spinner frame")
	}

	// Both lines should be different (different styling)
	if notConnectingLine == connectingLine {
		t.Errorf("expected different rendering when connecting vs settled")
	}
}
