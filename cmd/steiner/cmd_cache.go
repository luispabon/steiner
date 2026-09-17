package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func newCacheCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage application caches",
	}
	cmd.AddCommand(newCacheRefreshCommand())
	return cmd
}

func newCacheRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Refresh model metadata and provider model caches",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var errs []error
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "## Model metadata"); err != nil {
				errs = append(errs, fmt.Errorf("model metadata refresh: write section header: %w", err))
			}
			if err := runModelMetadataRefresh(cmd, nil); err != nil {
				errs = append(errs, fmt.Errorf("model metadata refresh: %w", err))
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
				errs = append(errs, fmt.Errorf("model metadata refresh: write section separator: %w", err))
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "## Provider models"); err != nil {
				errs = append(errs, fmt.Errorf("provider model refresh: write section header: %w", err))
			}
			if err := runModelsRefresh(cmd, nil); err != nil {
				errs = append(errs, fmt.Errorf("provider model refresh: %w", err))
			}
			return errors.Join(errs...)
		},
	}
}
