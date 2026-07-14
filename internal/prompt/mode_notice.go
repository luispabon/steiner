package prompt

import "github.com/luispabon/steiner/internal/config"

// ModeNotice returns a bracketed execution mode notice for injection into user messages.
// The notice is mode-specific instructive text designed to be prepended to an outgoing user message.
func ModeNotice(mode config.ExecutionMode) string {
	switch mode {
	case config.ExecutionModePlan:
		return "[execution mode: plan] You are now in plan mode. Do not attempt writes outside .steiner/plans/; tools will deny them. Produce plan artifacts there only when asked for a plan; otherwise just discuss."
	case config.ExecutionModeBuild:
		return "[execution mode: build] You are now in build mode. Normal editing rules apply."
	default:
		return ""
	}
}
