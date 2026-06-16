package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
)

func newRootCommand() *cobra.Command {
	flags := &cliFlags{}

	rootCmd := &cobra.Command{
		Use:          "steiner",
		Version:      version,
		SilenceUsage: true,
		Args:         cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flags.resume != "" && flags.resume != strings.TrimSpace(flags.resume) {
				flags.resume = strings.TrimSpace(flags.resume)
			}
			resumeSet := cmd.Flags().Changed("resume")
			if resumeSet && flags.resume == "" {
				return runListSessions(cmd, flags)
			}
			if resumeSet && flags.resume != "" {
				if flags.exec {
					return fmt.Errorf("--resume unsupported with --exec mode")
				}
				return runInteractiveMode(cmd, flags)
			}
			if flags.exec {
				return runExecMode(cmd, flags, args)
			}
			if len(args) > 0 {
				return fmt.Errorf("unexpected arguments: %s", strings.Join(args, " "))
			}
			return runInteractiveMode(cmd, flags)
		},
	}

	rootCmd.PersistentFlags().StringVar(&flags.configPath, "config", "", "project config file path")
	rootCmd.PersistentFlags().StringVar(&flags.model, "model", "", "override selected model alias")
	rootCmd.PersistentFlags().BoolVar(&flags.verbose, "verbose", false, "enable verbose logging")
	rootCmd.PersistentFlags().BoolVar(&flags.exec, "exec", false, "run a single request and exit")
	rootCmd.PersistentFlags().StringVar(&flags.logFile, "log-file", "", "write full session logs to file")
	rootCmd.PersistentFlags().StringVar(&flags.compactionLogFile, "compaction-log-file", "", "write compaction logs to file")
	rootCmd.PersistentFlags().IntVar(&flags.maxTurns, "max-turns", 0, "maximum agent turns for --exec mode (0 uses config default)")
	rootCmd.PersistentFlags().BoolVar(&flags.enableStreaming, "enable-streaming", false, "enable streaming responses in --exec mode (default: non-streaming)")
	rootCmd.PersistentFlags().BoolVar(&flags.unsafe, "unsafe", false, "disable sandbox (bubblewrap) for tool execution")
	rootCmd.Flags().StringVar(&flags.resume, "resume", "", "resume a saved session by ID; omit value to list sessions")
	rootCmd.Flag("resume").NoOptDefVal = ""

	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newConfigCommand(flags))
	rootCmd.AddCommand(newToolsCommand(flags))
	rootCmd.AddCommand(newSkillsCommand(flags))
	rootCmd.AddCommand(newModelCommand(flags))
	rootCmd.AddCommand(newModelMetadataCommand())
	rootCmd.AddCommand(newUpdateCommand())
	return rootCmd
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the steiner version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Steiner v%s\n", version)
			return err
		},
	}
}

func newConfigCommand(flags *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the resolved configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := config.Load(config.LoadOptions{
				CLI: config.CLIOverrides{
					ConfigPath: flags.configPath,
					Model:      flags.model,
					Verbose:    flags.verbose,
				},
			})
			if err != nil {
				return err
			}

			data, err := yaml.Marshal(resolved)
			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), string(data))
			return err
		},
	}
}

func newToolsCommand(flags *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "tools",
		Short: "List configured tools",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := buildRuntime(cmd.Context(), cmd, flags)
			if err != nil {
				return err
			}
			defer closeRuntime(&rt)
			renderNames(output.NewStream(cmd.OutOrStdout()), "tools", rt.toolNames)
			return nil
		},
	}
}

func newSkillsCommand(flags *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "skills",
		Short: "List discovered skills",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := buildRuntime(cmd.Context(), cmd, flags)
			if err != nil {
				return err
			}
			defer closeRuntime(&rt)
			renderNames(output.NewStream(cmd.OutOrStdout()), "skills", rt.skillNames)
			return nil
		},
	}
}

func renderNames(stream *output.Stream, heading string, names []string) {
	if stream == nil {
		return
	}
	if len(names) == 0 {
		stream.Printf("no %s configured\n", heading)
		return
	}
	stream.Printf("%s:\n", heading)
	for _, name := range names {
		stream.Printf("  %s\n", name)
	}
}

func runListSessions(cmd *cobra.Command, _ *cliFlags) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}

	sessionPath := filepath.Join(homeDir, ".config", "steiner", "sessions")
	store, err := session.NewStore(sessionPath)
	if err != nil {
		return fmt.Errorf("load session store: %w", err)
	}

	entries, err := store.List()
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	out := output.NewStream(cmd.OutOrStdout())
	if len(entries) == 0 {
		out.Printf("no sessions\n")
		return nil
	}

	out.Printf("%-4s %-80s %-20s %-12s %s\n", "#", "Title", "Model", "Updated", "ID")
	for i, entry := range entries {
		title := entry.Title
		if len(title) > 80 {
			title = title[:77] + "..."
		}
		relTime := formatRelativeTime(entry.UpdatedAt)
		out.Printf("%-4d %-80s %-20s %-12s %s\n", i, title, entry.Model, relTime, entry.ID)
	}

	return nil
}

func formatRelativeTime(t time.Time) string {
	now := time.Now()
	diff := now.Sub(t)

	if diff < 0 {
		return "in future"
	}

	const (
		minute = 60 * time.Second
		hour   = 60 * minute
		day    = 24 * hour
		week   = 7 * day
	)

	switch {
	case diff < minute:
		return "just now"
	case diff < hour:
		return fmt.Sprintf("%dm ago", int(diff/minute))
	case diff < day:
		return fmt.Sprintf("%dh ago", int(diff/hour))
	case diff < week:
		return fmt.Sprintf("%dd ago", int(diff/day))
	default:
		return t.Format("Jan 2, 2006")
	}
}
