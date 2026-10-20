package tui

import (
	"github.com/luispabon/steiner/internal/provider"
)

func cloneModelEntries(src []ModelEntry) []ModelEntry {
	if len(src) == 0 {
		return nil
	}
	entries := make([]ModelEntry, len(src))
	for i, entry := range src {
		entries[i] = entry
		entries[i].SupportedEfforts = append([]string(nil), entry.SupportedEfforts...)
	}
	return entries
}

func (m *Model) modelPickerEntries() []ModelEntry {
	if len(m.modelEntries) > 0 {
		return cloneModelEntries(m.modelEntries)
	}
	entries := make([]ModelEntry, len(m.modelNames))
	for i, name := range m.modelNames {
		entries[i] = ModelEntry{Ref: name, Display: name, Current: name == m.primaryModel}
	}
	return entries
}

func (m *Model) reasoningCapabilitiesForEntry(entry ModelEntry) provider.ReasoningCapabilities {
	if caps, ok := m.modelReasoningCapabilities[entry.Ref]; ok {
		return caps
	}
	return provider.ReasoningCapabilities{SupportedEfforts: append([]string(nil), entry.SupportedEfforts...)}
}

func cloneModelReasoningCapabilities(src map[string]provider.ReasoningCapabilities) map[string]provider.ReasoningCapabilities {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]provider.ReasoningCapabilities, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneModelContexts(src map[string]int) map[string]int {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]int, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func resolveModelBadge(backend string, aliases, reasoningLabels map[string]string) (string, string) {
	alias := backend
	if a, ok := aliases[backend]; ok {
		alias = a
	}
	return alias, reasoningLabels[alias]
}

// reasoningSidebarLabel derives the sidebar/picker label for a model's
// reasoning state: the effective effort when set, "default" when
// the model is reasoning-capable but uses no explicit effort, or "" when the
// model has no reasoning capability at all.
func reasoningSidebarLabel(effort string, caps provider.ReasoningCapabilities) string {
	if effort != "" {
		return effort
	}
	if len(caps.SupportedEfforts) > 0 {
		return "default"
	}
	return ""
}

// newReasoningLabels seeds the per-alias sidebar/picker reasoning label from
// each model's config-declared effective effort and reasoning capabilities,
// so a configured effort is visible from startup rather than only after a
// picker selection.
func newReasoningLabels(efforts map[string]string, caps map[string]provider.ReasoningCapabilities) map[string]string {
	labels := make(map[string]string, len(caps))
	for name, c := range caps {
		if label := reasoningSidebarLabel(efforts[name], c); label != "" {
			labels[name] = label
		}
	}
	return labels
}
