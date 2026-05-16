package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

func newModelCommand(flags *cliFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model",
		Short: "Model inspection commands",
	}
	cmd.AddCommand(newModelInspectCommand(flags))
	return cmd
}

func newModelInspectCommand(flags *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <alias>",
		Short: "Show resolved configuration for a model alias",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]
			cfg, err := config.Load(config.LoadOptions{
				CLI: config.CLIOverrides{
					ConfigPath:  flags.configPath,
					Model:       flags.model,
					Verbose:     flags.verbose,
					ContextMode: config.ContextMode(flags.contextMode),
				},
			})
			if err != nil {
				return err
			}
			rm, err := provider.ResolveWithDiscovery(cfg, alias, nil)
			if err != nil {
				return err
			}
			return printModelInspect(cmd.OutOrStdout(), rm)
		},
	}
}

func printModelInspect(out io.Writer, rm provider.ResolvedModel) error {
	fmt.Fprintf(out, "alias: %s\n", rm.Alias)
	fmt.Fprintf(out, "provider: %s\n", rm.ProviderAlias)
	fmt.Fprintf(out, "backend_id: %s\n", rm.BackendModelID)
	fmt.Fprintf(out, "metadata_source: %s\n", rm.MetadataSource)
	fmt.Fprintf(out, "confidence: %s\n", rm.Confidence)
	fmt.Fprintf(out, "limits:\n")
	fmt.Fprintf(out, "  context_window: %d\n", rm.EffectiveLimits.ContextWindow)
	fmt.Fprintf(out, "  max_output_tokens: %d\n", rm.EffectiveLimits.MaxOutputTokens)
	fmt.Fprintf(out, "  max_input_tokens: %d\n", rm.EffectiveLimits.MaxInputTokens)
	fmt.Fprintf(out, "  output_reserve_tokens: %d\n", rm.EffectiveLimits.OutputReserveTokens)
	fmt.Fprintf(out, "  safety_margin_tokens: %d\n", rm.EffectiveLimits.SafetyMarginTokens)
	fmt.Fprintf(out, "  summary_max_tokens: %d\n", rm.EffectiveLimits.SummaryMaxTokens)
	fmt.Fprintf(out, "  compaction_threshold: %.2f\n", rm.EffectiveLimits.CompactionThreshold)
	if len(rm.Params) > 0 {
		fmt.Fprintf(out, "params: %v\n", rm.Params)
	}
	if len(rm.ExtraParams) > 0 {
		fmt.Fprintf(out, "extra_params: %v\n", rm.ExtraParams)
	}
	if len(rm.Warnings) > 0 {
		fmt.Fprintf(out, "warnings:\n")
		for _, warn := range rm.Warnings {
			fmt.Fprintf(out, "  - %s\n", warn)
		}
	}
	return nil
}
