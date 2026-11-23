package main

import (
	"fmt"
	"io"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// startupLogger prints one console line per interactive startup step. Only
// runInteractiveMode creates one, and it writes to stderr, which may be piped;
// a disabled logger prints nothing.
type startupLogger struct {
	w          io.Writer
	enabled    bool
	labelStyle lipgloss.Style
}

func newStartupLogger(w io.Writer) *startupLogger {
	return &startupLogger{
		w:       w,
		enabled: output.SupportsANSI(w),
		// accentColor reads the user preference file, so resolve it once.
		labelStyle: lipgloss.NewStyle().Foreground(accentColor()).Bold(true),
	}
}

// step prints "  <check> <label> [detail]". It is safe to call on a nil
// logger because every call site uses the possibly-nil flag value.
func (l *startupLogger) step(label, detail string) {
	if l == nil || !l.enabled || l.w == nil {
		return
	}
	labelText := l.labelStyle.Render(label)
	if detail == "" {
		// Console output is best-effort; a failed write must not abort startup.
		_, _ = fmt.Fprintf(l.w, "  %s %s\n", checkMark(), labelText)
		return
	}
	detailText := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgDim)).Render(detail)
	// Same best-effort contract as above.
	_, _ = fmt.Fprintf(l.w, "  %s %s %s\n", checkMark(), labelText, detailText)
}

// pending prints "  … <label>" while a blocking startup step runs. It shares
// step's nil-receiver, disabled, and best-effort write contract.
func (l *startupLogger) pending(label string) {
	if l == nil || !l.enabled || l.w == nil {
		return
	}
	// Console output is best-effort; a failed write must not abort startup.
	_, _ = fmt.Fprintf(l.w, "  … %s\n", l.labelStyle.Render(label))
}

// trustSourceDetail names how project trust was resolved, or returns empty
// when this run has not resolved it.
func trustSourceDetail(flags *cliFlags) string {
	if flags == nil || !flags.trustResolved {
		return ""
	}
	return "project trusted (" + flags.trustSource + ")"
}

// mcpStartupDetail counts enabled MCP servers and notes a background connect.
func mcpStartupDetail(cfg config.Config, async bool) string {
	detail := fmt.Sprintf("%d server(s)", countEnabledMCPServers(cfg.MCP.Servers))
	if async {
		detail += " (background)"
	}
	return detail
}

// lspStartupDetail counts enabled language servers. Servers start on first
// use, so this is configuration only.
func lspStartupDetail(cfg config.Config) string {
	return fmt.Sprintf("%d configured (start on first use)", countEnabledLSPServers(cfg.LSP.Servers))
}

func countEnabledMCPServers(servers map[string]config.MCPServerConfig) int {
	count := 0
	for _, srv := range servers {
		if srv.Enabled {
			count++
		}
	}
	return count
}

func countEnabledLSPServers(servers map[string]config.LSPServerConfig) int {
	count := 0
	for _, srv := range servers {
		if srv.Enabled {
			count++
		}
	}
	return count
}
