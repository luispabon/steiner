package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

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
					ConfigPath: flags.configPath,
					Model:      flags.model,
					Verbose:    flags.verbose,
					Unsafe:     flags.unsafe,
				},
			})
			if err != nil {
				return err
			}
			rm, err := provider.ResolveWithDiscovery(cfg, alias, runtimeHTTPClient())
			if err != nil {
				return err
			}
			return printModelInspect(cmd.OutOrStdout(), rm)
		},
	}
}

func printModelInspect(out io.Writer, rm provider.ResolvedModel) error {
	if _, err := fmt.Fprintf(out,
		"alias: %s\nprovider: %s\nbackend_id: %s\nconfidence: %s\nconfigured_provider_type: %s\neffective_provider_type: %s\neffective_transport: %s\nmetadata_source: %s\ntransport_override_reason: %s\n",
		rm.Alias,
		rm.ProviderAlias,
		rm.BackendModelID,
		rm.Confidence,
		rm.ProviderConfig.Type,
		rm.EffectiveProviderType,
		rm.EffectiveTransport,
		rm.MetadataSource,
		rm.TransportOverrideReason,
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out,
		"limits:\n  source: %s\n  confidence: %s\n  context_window: %d\n  max_output_tokens: %d\n",
		rm.MetadataSource,
		rm.Confidence,
		rm.EffectiveLimits.ContextWindow,
		rm.EffectiveLimits.MaxOutputTokens,
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out,
		"derived_policy:\n  compaction_threshold: %.2f\n  estimator_pad_tokens: %d\n  normal_summary_token_budget: %d\n  emergency_summary_token_budget: %d\n",
		rm.EffectiveLimits.CompactionThreshold,
		rm.EffectiveLimits.EstimatorPadTokens,
		rm.EffectiveLimits.NormalSummaryMaxTokens,
		rm.EffectiveLimits.EmergencySummaryMaxTokens,
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out,
		"params: %s\nextra_params: %s\nprompt_suffix: %q\ntokenizer:\n  strategy: %s\n  confidence: %s\n",
		formatJSONMap(rm.Params),
		formatJSONMap(rm.ExtraParams),
		rm.PromptSuffix,
		rm.TokenizerStrategy,
		rm.TokenizerConfidence,
	); err != nil {
		return err
	}
	if err := printModelInspectReasoning(out, rm); err != nil {
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

// printModelInspectReasoning prints a reasoning: diagnostic block describing
// the resolved reasoning capabilities and effort selection for rm.
func printModelInspectReasoning(out io.Writer, rm provider.ResolvedModel) error {
	_, err := fmt.Fprintf(out,
		"reasoning:\n  supported_efforts: %s\n  provider_default_effort: %s\n  configured_effort: %s\n  effective_effort: %s\n  source: %s\n  confidence: %s\n",
		formatEffortList(rm.Reasoning.SupportedEfforts),
		formatOptionalEffort(rm.Reasoning.ProviderDefaultEffort, "unknown"),
		formatOptionalEffort(rm.ReasoningConfiguredEffort, "none"),
		formatOptionalEffort(rm.ReasoningEffectiveEffort, "none (provider default applies, reasoning field omitted from requests)"),
		formatOptionalEffort(rm.Reasoning.Source, "unknown"),
		formatOptionalEffort(rm.Reasoning.Confidence, "unknown"),
	)
	return err
}

func formatEffortList(efforts []string) string {
	if len(efforts) == 0 {
		return "none"
	}
	return "[" + strings.Join(efforts, ", ") + "]"
}

func formatOptionalEffort(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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
