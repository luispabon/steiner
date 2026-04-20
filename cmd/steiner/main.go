package main

import (
	"fmt"
	"os"

	"github.com/luispabon/steiner/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const version = "dev"

type cliFlags struct {
	configPath string
	model      string
	verbose    bool
	exec       bool
}

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	flags := &cliFlags{}

	rootCmd := &cobra.Command{
		Use:           "steiner",
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	rootCmd.PersistentFlags().StringVar(&flags.configPath, "config", "", "project config file path")
	rootCmd.PersistentFlags().StringVar(&flags.model, "model", "", "override provider model")
	rootCmd.PersistentFlags().BoolVar(&flags.verbose, "verbose", false, "enable verbose logging")
	rootCmd.PersistentFlags().BoolVar(&flags.exec, "exec", false, "stub execution flag")

	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newConfigCommand(flags))

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
