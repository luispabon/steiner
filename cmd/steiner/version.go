package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/prefs"
	"github.com/luispabon/steiner/internal/tui/theme"
)

var channel string
var versionPanelString string

func init() {
	if version == "dev" || strings.HasPrefix(version, "dev-") {
		channel = "dev"
	} else {
		channel = "stable"
	}
	versionPanelString = precomputedVersionPanelString()
}

// accentColor loads the user preference and returns the lipgloss color.
// Falls back to theme.AccentPresets["amber"] if prefs can't be loaded.
func accentColor() lipgloss.Color {
	p, err := prefs.Load()
	if err != nil {
		return lipgloss.Color(theme.AccentPresets["amber"])
	}
	hex := theme.AccentPresets[p.Accent]
	if hex == "" {
		hex = theme.AccentPresets["amber"]
	}
	return lipgloss.Color(hex)
}

// valueStyle renders text in bold + accent foreground.
func valueStyle(text string) string {
	return lipgloss.NewStyle().Foreground(accentColor()).Bold(true).Render(text)
}

// labelStyle returns a dimmed, fixed-width label style (width 10).
func labelStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgDim)).Width(10)
}

// checkMark returns a green ✔ glyph styled with theme.Added.
func checkMark() string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Added)).Render("✔")
}

// crossMark returns a red ✗ glyph styled with theme.Removed.
func crossMark() string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Removed)).Render("✗")
}

// printVersionPanel writes a 5-line panel: version, commit, buildDate, goVersion, channel.
func printVersionPanel(w io.Writer) {
	lbl := labelStyle()
	_, _ = fmt.Fprintf(w, "  %s %s\n", lbl.Render("version"), valueStyle(version))
	_, _ = fmt.Fprintf(w, "  %s %s\n", lbl.Render("commit"), valueStyle(commit))
	_, _ = fmt.Fprintf(w, "  %s %s\n", lbl.Render("built"), valueStyle(buildDate))
	_, _ = fmt.Fprintf(w, "  %s %s\n", lbl.Render("go"), valueStyle(goVersion))
	_, _ = fmt.Fprintf(w, "  %s %s\n", lbl.Render("channel"), valueStyle(channel))
}

// printVersionBlock writes 2 lines: current version and latest version.
func printVersionBlock(w io.Writer, current, latest string) {
	lbl := labelStyle()
	_, _ = fmt.Fprintf(w, "  %s %s\n", lbl.Render("current"), valueStyle(current))
	_, _ = fmt.Fprintf(w, "  %s %s\n", lbl.Render("latest"), valueStyle(latest))
}

// precomputedVersionPanelString returns the full version panel as a string.
// Called once in init() and cached in versionPanelString.
func precomputedVersionPanelString() string {
	var buf bytes.Buffer
	printVersionPanel(&buf)
	return buf.String()
}
