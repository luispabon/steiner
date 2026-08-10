package prompt

import "github.com/luispabon/steiner/internal/config"

// ModeNotice returns a bracketed execution mode notice for injection into user messages.
// The notice is mode-specific instructive text designed to be prepended to an outgoing user message.
func ModeNotice(mode config.ExecutionMode) string {
	switch mode {
	case config.ExecutionModePlan:
		return "[execution mode: plan] Plan mode. Project edits are restricted; plan artifacts may be written under .steiner/plans/. Discuss proposals in conversation, do not edit while requirements are moving, and write a handoff plan only when ready."
	case config.ExecutionModeBuild:
		return "[execution mode: build] Build mode. Normal workspace editing."
	default:
		return ""
	}
}
