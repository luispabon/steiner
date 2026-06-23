package main

import (
	"bytes"
	"fmt"
	"image/color"
	"io"
	"strings"

	"charm.land/lipgloss/v2"

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
func accentColor() color.Color {
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

// checkMark returns a green ✔ glyph styled with theme.Added.
func checkMark() string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Added)).Render("✔")
}

// crossMark returns a red ✗ glyph styled with theme.Removed.
func crossMark() string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Removed)).Render("✗")
}

// printVersionLine writes a single key/value line.
// The key is bold with the accent colour; the value uses the default foreground.
func printVersionLine(w io.Writer, label, value string) {
	keyStyle := lipgloss.NewStyle().Foreground(accentColor()).Bold(true).Width(10)
	valStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	_, _ = fmt.Fprintf(w, "  %s %s\n", keyStyle.Render(label), valStyle.Render(value))
}

// printVersionPanel writes a 5-line panel: version, commit, buildDate, goVersion, channel.
func printVersionPanel(w io.Writer) {
	printVersionLine(w, "version", version)
	printVersionLine(w, "commit", commit)
	printVersionLine(w, "built", buildDate)
	printVersionLine(w, "go", goVersion)
	printVersionLine(w, "channel", channel)
}

// precomputedVersionPanelString returns the full version panel as a string.
// Called once in init() and cached in versionPanelString.
func precomputedVersionPanelString() string {
	var buf bytes.Buffer
	printVersionPanel(&buf)
	return buf.String()
}
