package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/update"
)

var updateFunc = update.Channel

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
			// 1. Dev build warning (re-enabled).
			// If this is a dev build and --dev is not set, warn and exit.
			if isDevBuild(version) {
				devSet := devFlag || devFlagFromCmd(cmd)
				if !devSet {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Warning: dev builds cannot check for stable updates. Use --dev to update to the latest dev build.")
					return nil
				}
			}

			// 2. Determine channel.
			channel := "stable"
			if devFlagFromCmd(cmd) || devFlag {
				channel = "dev"
			}

			token := os.Getenv("STEINER_GITHUB_TOKEN")

			// 3. Print current version before the download spinner.
			printVersionLine(cmd.OutOrStdout(), "current", version)

			// 4. Start spinner. Always deferred-stop to prevent goroutine leaks.
			sp := NewSpinner(cmd.OutOrStdout(), "Downloading…")
			sp.Start()
			defer sp.Stop(false, "aborted")

			// 5. Call updateFunc (fetch + download + verify + replace).
			latestVer, err := updateFunc(cmd.Context(), version, "luispabon", "steiner", token, channel)

			// 6. Handle results.
			if errors.Is(err, update.ErrUpToDate) {
				sp.Stop(true, "already up to date")
				printVersionLine(cmd.OutOrStdout(), "latest", latestVer)
				return nil
			}
			if err != nil {
				sp.Stop(false, err.Error())
				return err
			}
			sp.Stop(true, "updated to "+latestVer)
			printVersionLine(cmd.OutOrStdout(), "latest", latestVer)
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
