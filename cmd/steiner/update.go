package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/tui/prefs"
	"github.com/luispabon/steiner/internal/tui/theme"
	"github.com/luispabon/steiner/internal/update"
)

var updateFunc = update.Update

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

func newUpdateCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "update",
		Short:   "Update steiner to the latest release",
		Aliases: []string{"upgrade"},
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if version == "dev" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Warning: dev build cannot self-update (no version to compare against).")
				return nil
			}
			token := os.Getenv("STEINER_GITHUB_TOKEN")
			latestVer, err := updateFunc(cmd.Context(), version, "luispabon", "steiner", token)
			if errors.Is(err, update.ErrUpToDate) {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "steiner is already up to date")
				printVersionInfo(cmd.OutOrStdout(), version, latestVer)
				return nil
			}
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "steiner updated successfully to %s\n", versionStyle(latestVer))
			return nil
		},
	}
}

func versionStyle(v string) string {
	accent := accentColor()
	return lipgloss.NewStyle().Foreground(accent).Bold(true).Render(v)
}

func printVersionInfo(w io.Writer, current, latest string) {
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgDim))
	const labelWidth = 18
	currentLabel := labelStyle.Width(labelWidth).Render("current version")
	latestLabel := labelStyle.Width(labelWidth).Render("latest version")

	_, _ = fmt.Fprintf(w, "\n  %s %s\n  %s %s\n",
		currentLabel, versionStyle(current),
		latestLabel, versionStyle(latest),
	)
}
