package agent

import (
	"strings"
)

// Scratchpad holds the persisted working state for the scaffold-managed task
// snapshot. The model-written fields are intent, decisions, open, and next.
// The remaining fields are maintained by steiner.
type Scratchpad struct {
	Intent    string
	Decisions string
	Open      string
	Next      string

	WorkingFile     string
	LastAction      string
	SessionState    string
	TrackedFiles    []string
	RecentToolCalls []string

	// Legacy fields are retained for compatibility while the rest of the code
	// base migrates to the reduced scratchpad schema.
	Goal  string
	Plan  string
	Step  string
	Files string
}

// Render returns the scratchpad as a plain-text block for injection as a
// synthetic user message.
func (s Scratchpad) Render() string {
	lines := []string{"[Current task state]"}

	if session := strings.TrimSpace(s.SessionState); session != "" {
		lines = append(lines, session)
	}
	if workingFile := strings.TrimSpace(s.WorkingFile); workingFile != "" {
		lines = append(lines, "working file: "+workingFile)
	}
	if lastAction := strings.TrimSpace(s.LastAction); lastAction != "" {
		lines = append(lines, "last action: "+lastAction)
	}
	if len(s.TrackedFiles) > 0 {
		lines = append(lines, "tracked files:")
		for _, item := range s.TrackedFiles {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				lines = append(lines, "- "+trimmed)
			}
		}
	}
	if len(s.RecentToolCalls) > 0 {
		lines = append(lines, "recent tool calls:")
		for _, item := range s.RecentToolCalls {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				lines = append(lines, "- "+trimmed)
			}
		}
	}

	intent := strings.TrimSpace(s.Intent)
	if intent == "" {
		intent = strings.TrimSpace(strings.Join(nonEmptyStrings([]string{s.Goal, s.Plan, s.Step}), " "))
	}
	lines = append(lines, "intent: "+intent)
	lines = append(lines, "decisions: "+strings.TrimSpace(s.Decisions))
	lines = append(lines, "open: "+strings.TrimSpace(s.Open))
	lines = append(lines, "next: "+strings.TrimSpace(s.Next))
	return strings.Join(lines, "\n")
}

func nonEmptyStrings(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
