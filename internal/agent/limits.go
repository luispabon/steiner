package agent

import "time"

// Limits constrains a run by turns, tokens, and tool execution time.
type Limits struct {
	MaxTurns    int
	MaxTokens   int
	ToolTimeout time.Duration
}
