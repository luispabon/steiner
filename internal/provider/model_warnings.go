package provider

import "fmt"

// deriveWarnings builds user-facing warnings from final resolved facts and
// source-level errors. Warnings are derived only from the final state, never
// from individual lookups that lost precedence to a higher-priority source.
func deriveWarnings(ref modelRef, facts ModelFacts, notes map[factField][]string, sourceErrs []string) []string {
	var warnings []string

	for _, sourceErr := range sourceErrs {
		warnings = append(warnings, "Model metadata warning: "+sourceErr+".")
	}

	if facts.ContextWindow.Source == FactSourceFallback {
		lookupReason := ""
		if ctxNotes := notes[fieldContextWindow]; len(ctxNotes) > 0 {
			lookupReason = " models.dev lookup degraded: " + ctxNotes[0] + "."
		}
		if ref.IsAlias {
			warnings = append(warnings, fmt.Sprintf(
				"Model metadata warning: %s/%s has unknown context limits.%s Using conservative fallback: context_window=%d, max_output_tokens=%d. Set models.%s.advanced.limits.context_window to remove this warning.",
				ref.Alias, ref.BackendModelID, lookupReason, facts.ContextWindow.Value, facts.MaxOutputTokens.Value, ref.Alias,
			))
		} else {
			warnings = append(warnings, fmt.Sprintf(
				"Model metadata warning: %s has unknown context limits.%s Using conservative fallback: context_window=%d, max_output_tokens=%d.",
				ref.Alias, lookupReason, facts.ContextWindow.Value, facts.MaxOutputTokens.Value,
			))
		}
	}

	return warnings
}
