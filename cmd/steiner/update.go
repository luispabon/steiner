package main

import (
	"fmt"
	"os"

	"github.com/luispabon/steiner/internal/update"
	"github.com/spf13/cobra"
)

var updateFunc = update.Update

func newUpdateCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "update",
		Short:   "Update steiner to the latest release",
		Aliases: []string{"upgrade"},
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if version == "dev" {
				fmt.Fprintln(cmd.ErrOrStderr(), "Warning: dev build cannot self-update (no version to compare against).")
				return nil
			}
			token := os.Getenv("STEINER_GITHUB_TOKEN")
			err := updateFunc(cmd.Context(), version, "luispabon", "steiner", token)
			if err == update.ErrUpToDate {
				fmt.Fprintln(cmd.OutOrStdout(), "steiner is already up to date")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "steiner updated successfully to v%s\n", version)
			return nil
		},
	}
}
