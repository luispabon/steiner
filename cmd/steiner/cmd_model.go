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
			httpClient := runtimeHTTPClient()
			modelCatalog, _, _ := buildModelCatalogService(&cfg, httpClient)
			resolver := provider.NewResolver(provider.ResolverOptions{
				HTTPClient: httpClient,
				Catalog:    newCatalogMetadataAdapter(modelCatalog, &cfg),
			})
			rm, err := resolver.Resolve(cmd.Context(), cfg, alias)
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
	params, err := formatJSONMap(rm.Params)
	if err != nil {
		return fmt.Errorf("format params: %w", err)
	}
	extraParams, err := formatJSONMap(rm.ExtraParams)
	if err != nil {
		return fmt.Errorf("format extra params: %w", err)
	}
	if _, err := fmt.Fprintf(out,
		"params: %s\nextra_params: %s\nprompt_suffix: %q\ntokenizer:\n  strategy: %s\n  confidence: %s\n",
		params,
		extraParams,
		rm.PromptSuffix,
		rm.TokenizerStrategy,
		rm.TokenizerConfidence,
	); err != nil {
		return err
	}
	if err := printModelInspectReasoning(out, rm); err != nil {
		return err
	}
	if err := printModelInspectFacts(out, rm); err != nil {
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

// printModelInspectFacts prints a facts: diagnostic block describing each
// per-fact resolution result and its provenance from rm.Facts.
func printModelInspectFacts(out io.Writer, rm provider.ResolvedModel) error {
	if _, err := fmt.Fprint(out, "facts:\n"); err != nil {
		return err
	}
	lines := []string{
		formatFactLine("context_window", rm.Facts.ContextWindow.Known, rm.Facts.ContextWindow.Value, rm.Facts.ContextWindow.Source, rm.Facts.ContextWindow.Confidence, rm.Facts.ContextWindow.Note),
		formatFactLine("max_output_tokens", rm.Facts.MaxOutputTokens.Known, rm.Facts.MaxOutputTokens.Value, rm.Facts.MaxOutputTokens.Source, rm.Facts.MaxOutputTokens.Confidence, rm.Facts.MaxOutputTokens.Note),
		formatFactLine("vision", rm.Facts.Vision.Known, rm.Facts.Vision.Value, rm.Facts.Vision.Source, rm.Facts.Vision.Confidence, rm.Facts.Vision.Note),
		formatFactLine("reasoning_efforts", rm.Facts.ReasoningEfforts.Known, formatEffortList(rm.Facts.ReasoningEfforts.Value), rm.Facts.ReasoningEfforts.Source, rm.Facts.ReasoningEfforts.Confidence, rm.Facts.ReasoningEfforts.Note),
		formatFactLine("reasoning_echo_back", rm.Facts.ReasoningEchoBack.Known, rm.Facts.ReasoningEchoBack.Value, rm.Facts.ReasoningEchoBack.Source, rm.Facts.ReasoningEchoBack.Confidence, rm.Facts.ReasoningEchoBack.Note),
		formatFactLine("transport", rm.Facts.Transport.Known, rm.Facts.Transport.Value, rm.Facts.Transport.Source, rm.Facts.Transport.Confidence, rm.Facts.Transport.Note),
	}
	for _, line := range lines {
		if _, err := fmt.Fprintf(out, "  %s\n", line); err != nil {
			return err
		}
	}
	return nil
}

// formatFactLine renders a single line of the facts: block for one resolved
// fact. value is rendered with fmt.Sprint; known=false renders "unknown"
// regardless of value to avoid confusing an unset value with a real zero.
func formatFactLine[T any](name string, known bool, value T, source provider.FactSource, confidence, note string) string {
	valueStr := "unknown"
	if known {
		valueStr = fmt.Sprint(value)
	}
	line := fmt.Sprintf("%s: value=%s source=%s confidence=%s",
		name, valueStr, formatOptionalEffort(string(source), "unknown"), formatOptionalEffort(confidence, "unknown"))
	if note != "" {
		line += " note=" + note
	}
	return line
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

func formatJSONMap(values map[string]any) (string, error) {
	if len(values) == 0 {
		return "{}", nil
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
