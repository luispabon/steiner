package agent

import "time"

type Limits struct {
	MaxTurns    int
	MaxTokens   int
	ToolTimeout time.Duration
}
