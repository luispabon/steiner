package prompt

import (
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

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

// StripModeNotice removes a leading execution-mode notice prefix (as produced
// by ModeNotice + "\n\n") from persisted user message content, if present.
// Matches against the exact known notice strings for plan and build mode — a
// small closed set, not a general pattern.
func StripModeNotice(content string) string {
	for _, mode := range []config.ExecutionMode{config.ExecutionModePlan, config.ExecutionModeBuild} {
		prefix := ModeNotice(mode) + "\n\n"
		if strings.HasPrefix(content, prefix) {
			return strings.TrimPrefix(content, prefix)
		}
	}
	return content
}
