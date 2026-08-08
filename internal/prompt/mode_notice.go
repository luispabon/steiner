package prompt

import "github.com/luispabon/steiner/internal/config"

// ModeNotice returns a bracketed execution mode notice for injection into user messages.
// The notice is mode-specific instructive text designed to be prepended to an outgoing user message.
func ModeNotice(mode config.ExecutionMode) string {
	switch mode {
	case config.ExecutionModePlan:
		return "[execution mode: plan] Plan mode. Read-only; discuss, do not write. Plan files are for handoff only."
	case config.ExecutionModeBuild:
		return "[execution mode: build] Build mode. Normal editing rules apply."
	default:
		return ""
	}
}
