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

func newWorktreesCommand(flags *cliFlags) *cobra.Command {
	var list bool
	var prune string
	var pruneAll bool

	cmd := &cobra.Command{
		Use:   "worktrees",
		Short: "List or prune code delegation worktrees",
		Long:  "Manage code worktrees provisioned by the code sub-agent delegation.\n\nUse --list to show all provisioned worktrees, --prune <id> to remove a specific worktree by agent ID, or --prune-all to remove all worktrees.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd.Flags().StringVar(&prune, "prune", "", "remove a worktree by agent ID")
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

	stream.Printf("%-40s %-30s %s\n", "Agent ID", "Branch", "Path")
	for _, wt := range worktrees {
		agentID := filepath.Base(wt.Path)
		stream.Printf("%-40s %-30s %s\n", agentID, wt.Branch, wt.Path)
	}

	return nil
}

func runWorktreesPrune(cmd *cobra.Command, projectRoot, agentID string) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return fmt.Errorf("--prune requires a non-empty agent ID")
	}

	err := delegation.PruneCodeWorktree(cmd.Context(), projectRoot, agentID)
	if err != nil {
		return fmt.Errorf("prune code worktree: %w", err)
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(), "removed worktree: %s\n", agentID)
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
