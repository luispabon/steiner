package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/tui/theme"
	"github.com/luispabon/steiner/internal/update"
)

var checkFunc = update.Check
var applyFunc = update.Channel

// isDevBuild returns true if the version is a dev build that cannot be
// semver-compared: the literal "dev" or any version prefixed with "dev-".
func isDevBuild(v string) bool {
	return v == "dev" || strings.HasPrefix(v, "dev-")
}

// displayVersion formats a version string for display, adding a "v" prefix
// if it looks like a semver without one.
func displayVersion(v string) string {
	if isDevBuild(v) {
		return v
	}
	if !strings.HasPrefix(v, "v") && !strings.HasPrefix(v, "V") {
		return "v" + v
	}
	return v
}

// printChannelSwitchWarning prints a warning when switching between release
// channels, using the same layout as printVersionLine.
func printChannelSwitchWarning(w io.Writer, currentChannel, requestedChannel string) {
	accent := accentColor()
	labelStyle := lipgloss.NewStyle().Foreground(accent).Bold(true).Width(10)
	valStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	warnStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
	_, _ = fmt.Fprintf(w, "  %s %s%s%s%s%s\n",
		labelStyle.Render("⚠ notice"),
		valStyle.Render("switching from "),
		warnStyle.Render(currentChannel),
		valStyle.Render(" to "),
		warnStyle.Render(requestedChannel),
		valStyle.Render(" — this replaces your current build"),
	)
}

func newUpdateCommand() *cobra.Command {
	var devFlag bool

	cmd := &cobra.Command{
		Use:     "update [version]",
		Short:   "Update steiner to the latest release, or to a specific version",
		Aliases: []string{"upgrade"},
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// 1. Extract target version from args.
			targetVersion := ""
			if len(args) > 0 {
				targetVersion = args[0]
			}

			// 2. Determine requested channel.
			requestedChannel := "stable"
			if devFlag || devFlagFromCmd(cmd) {
				requestedChannel = "dev"
			}

			// 3. Validate: --dev and target version cannot be used together.
			if targetVersion != "" && requestedChannel == "dev" {
				return fmt.Errorf("--dev and a target version cannot be used together")
			}

			// 4. Channel switch warning.
			if channel != requestedChannel {
				printChannelSwitchWarning(cmd.OutOrStdout(), channel, requestedChannel)
			}

			token := os.Getenv("STEINER_GITHUB_TOKEN")

			// 5. Check phase.
			sp := NewSpinner(cmd.OutOrStdout(), "checking version")
			sp.Start()
			latestVer, needsUpdate, err := checkFunc(cmd.Context(), version, "luispabon", "steiner", token, requestedChannel, targetVersion)
			sp.Clear()

			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s %s\n", crossMark(), err.Error())
				return err
			}

			// 6. Print version info.
			printVersionLine(cmd.OutOrStdout(), "Current:", displayVersion(version))
			printVersionLine(cmd.OutOrStdout(), "Latest:", displayVersion(latestVer))

			// 7. Already up to date.
			if !needsUpdate {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  "+checkMark()+" Up to date")
				return nil
			}

			// 8. Apply phase.
			sp2 := NewSpinner(cmd.OutOrStdout(), "updating...")
			sp2.Start()
			_, err = applyFunc(cmd.Context(), version, "luispabon", "steiner", token, requestedChannel, targetVersion)

			if err != nil {
				sp2.Stop(false, err.Error())
				return err
			}

			sp2.Stop(true, "updated")
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
