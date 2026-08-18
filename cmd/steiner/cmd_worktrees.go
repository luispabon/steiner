package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
)

func newWorktreesCommand(_ *cliFlags) *cobra.Command {
	var list bool
	var prune string
	var pruneAll bool

	cmd := &cobra.Command{
		Use:   "worktrees",
		Short: "List or prune code delegation worktrees",
		Long:  "Manage code worktrees provisioned by the code sub-agent delegation.\n\nUse --list to show all provisioned worktrees, --prune <id> to remove a specific worktree by its ID (shown in --list output), or --prune-all to remove all worktrees.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flagsSet := 0
			if list {
				flagsSet++
			}
			if prune != "" {
				flagsSet++
			}
			if pruneAll {
				flagsSet++
			}

			if flagsSet != 1 {
				return fmt.Errorf("specify exactly one of --list, --prune, or --prune-all")
			}

			projectRoot, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			if list {
				return runWorktreesList(cmd, projectRoot)
			}
			if prune != "" {
				return runWorktreesPrune(cmd, projectRoot, prune)
			}
			if pruneAll {
				return runWorktreesPruneAll(cmd, projectRoot)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&list, "list", false, "list all provisioned worktrees")
	cmd.Flags().StringVar(&prune, "prune", "", "remove a worktree by its ID (copy from --list output)")
	cmd.Flags().BoolVar(&pruneAll, "prune-all", false, "remove all provisioned worktrees")
	return cmd
}

func runWorktreesList(cmd *cobra.Command, projectRoot string) error {
	worktrees, err := delegation.ListCodeWorktrees(projectRoot)
	if err != nil {
		return fmt.Errorf("list code worktrees: %w", err)
	}

	stream := output.NewStream(cmd.OutOrStdout())
	if len(worktrees) == 0 {
		stream.Printf("no worktrees provisioned\n")
		return nil
	}

	delegationBase := filepath.Join(projectRoot, ".steiner", "worktrees")
	stream.Printf("%-50s %-30s %s\n", "ID", "Branch", "Path")
	for _, wt := range worktrees {
		relID, err := filepath.Rel(delegationBase, wt.Path)
		if err != nil {
			relID = wt.Path
		}
		stream.Printf("%-50s %-30s %s\n", relID, wt.Branch, wt.Path)
	}

	return nil
}

func runWorktreesPrune(cmd *cobra.Command, projectRoot, relID string) error {
	relID = strings.TrimSpace(relID)
	if relID == "" {
		return fmt.Errorf("--prune requires a non-empty worktree ID")
	}

	err := delegation.PruneCodeWorktree(cmd.Context(), projectRoot, relID)
	if err != nil {
		return fmt.Errorf("prune code worktree: %w", err)
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(), "removed worktree: %s\n", relID)
	return err
}

func runWorktreesPruneAll(cmd *cobra.Command, projectRoot string) error {
	// Get the count before pruning.
	worktrees, err := delegation.ListCodeWorktrees(projectRoot)
	if err != nil {
		return fmt.Errorf("list code worktrees: %w", err)
	}
	count := len(worktrees)

	// Prune all.
	err = delegation.PruneAllCodeWorktrees(cmd.Context(), projectRoot)
	if err != nil {
		return fmt.Errorf("prune all code worktrees: %w", err)
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(), "removed %d worktree(s)\n", count)
	return err
}
