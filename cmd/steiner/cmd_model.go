package main

import (
	"encoding/json"
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
	if _, err := fmt.Fprintf(out,
		"alias: %s\nprovider: %s\nbackend_id: %s\nconfidence: %s\nlimits:\n  source: %s\n  context_window: %d\n  max_output_tokens: %d\n  output_reserve_tokens: %d\n  safety_margin_tokens: %d\n  summary_max_tokens: %d\n  compaction_threshold: %.2f\nparams: %s\nextra_params: %s\ntokenizer:\n  strategy: %s\n  confidence: %s\n",
		rm.Alias,
		rm.ProviderAlias,
		rm.BackendModelID,
		rm.Confidence,
		rm.MetadataSource,
		rm.EffectiveLimits.ContextWindow,
		rm.EffectiveLimits.MaxOutputTokens,
		rm.EffectiveLimits.OutputReserveTokens,
		rm.EffectiveLimits.SafetyMarginTokens,
		rm.EffectiveLimits.SummaryMaxTokens,
		rm.EffectiveLimits.CompactionThreshold,
		formatJSONMap(rm.Params),
		formatJSONMap(rm.ExtraParams),
		rm.TokenizerStrategy,
		rm.TokenizerConfidence,
	); err != nil {
		return err
	}
	if len(rm.Warnings) > 0 {
		if _, err := fmt.Fprint(out, "warnings:\n"); err != nil {
			return err
		}
		for _, warn := range rm.Warnings {
			if _, err := fmt.Fprintf(out, "  - %s\n", warn); err != nil {
				return err
			}
		}
	}
	return nil
}

func formatJSONMap(values map[string]any) string {
	if len(values) == 0 {
		return "{}"
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "{}"
	}
	return string(data)
}
