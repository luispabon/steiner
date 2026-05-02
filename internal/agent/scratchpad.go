package agent

import "strings"

// Scratchpad holds the model's working state across turns.
// Decisions is steiner-managed (concatenated with byte cap).
// All other fields are model-owned and replaced each turn.
type Scratchpad struct {
	Goal      string
	Plan      string
	Step      string
	Decisions string // steiner-managed concat; model appends this turn's new decisions only
	Files     string // files model considers relevant; model-owned, not FileTracker
	Open      string
	Next      string
}

// Render returns the scratchpad as a plain-text block for injection as a
// synthetic user message.
func (s Scratchpad) Render() string {
	lines := []string{
		"[Current task state]",
		"goal: " + s.Goal,
		"plan: " + s.Plan,
		"step: " + s.Step,
		"decisions: " + s.Decisions,
		"files: " + s.Files,
		"open: " + s.Open,
		"next: " + s.Next,
	}
	return strings.Join(lines, "\n")
}
