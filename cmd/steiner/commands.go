package main

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newRootCommand() *cobra.Command {
	flags := &cliFlags{}

	rootCmd := &cobra.Command{
		Use:          "steiner",
		SilenceUsage: true,
		Args:         cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
	rootCmd.PersistentFlags().IntVar(&flags.maxTurns, "max-turns", 0, "maximum agent turns for --exec mode (0 uses config default)")

	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newConfigCommand(flags))
	rootCmd.AddCommand(newToolsCommand(flags))
	rootCmd.AddCommand(newSkillsCommand(flags))

	return rootCmd
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the steiner version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "steiner %s\n", version)
			return err
		},
	}
}

func newConfigCommand(flags *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the resolved configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := buildRuntime(cmd.Context(), cmd, flags)
			if err != nil {
				return err
			}
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
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := buildRuntime(cmd.Context(), cmd, flags)
			if err != nil {
				return err
			}
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
