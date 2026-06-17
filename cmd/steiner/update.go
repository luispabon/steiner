package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/tui/prefs"
	"github.com/luispabon/steiner/internal/tui/theme"
	"github.com/luispabon/steiner/internal/update"
)

var updateFunc = update.Channel

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

// isDevBuild returns true if the version is a dev build that cannot be
// semver-compared: the literal "dev" or any version prefixed with "dev-".
func isDevBuild(v string) bool {
	return v == "dev" || strings.HasPrefix(v, "dev-")
}

func newUpdateCommand() *cobra.Command {
	var devFlag bool

	cmd := &cobra.Command{
		Use:     "update",
		Short:   "Update steiner to the latest release",
		Aliases: []string{"upgrade"},
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token := os.Getenv("STEINER_GITHUB_TOKEN")
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Checking for updates…")

			// Subcommand --dev overrides the root --dev flag.
			channel := "stable"
			if rootDev := devFlagFromCmd(cmd); rootDev || devFlag {
				channel = "dev"
			}

			latestVer, err := updateFunc(cmd.Context(), version, "luispabon", "steiner", token, channel)
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

	cmd.Flags().BoolVar(&devFlag, "dev", false, "Update from the dev channel instead of the latest stable release")
	return cmd
}

// devFlagFromCmd returns the value of the root-level persistent --dev flag,
// or false if it was not registered. It is used so `steiner --dev update` and
// `steiner update --dev` both select the dev release channel.
func devFlagFromCmd(cmd *cobra.Command) bool {
	flag := cmd.Root().PersistentFlags().Lookup("dev")
	if flag == nil {
		return false
	}
	val, err := strconv.ParseBool(flag.Value.String())
	if err != nil {
		return false
	}
	return val
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
