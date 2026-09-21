package provider

import "strings"

// ModelEffortLabel joins a model reference and a reasoning effort as
// "ref/effort", returning ref alone when effort is empty. Both are trimmed.
func ModelEffortLabel(ref, effort string) string {
	ref = strings.TrimSpace(ref)
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return ref
	}
	return ref + "/" + effort
}
