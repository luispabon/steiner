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
				printVersionBlock(cmd.OutOrStdout(), version, latestVer)
				return nil
			}
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "steiner updated successfully to %s\n", valueStyle(latestVer))
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
