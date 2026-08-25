package agent

import "time"

// Limits constrains a run by turns, tokens, and tool execution time.
type Limits struct {
	MaxTurns    int
	MaxTokens   int
	TurnTimeout time.Duration
	ToolTimeout time.Duration

	// ModelCallTimeout bounds a single provider call, not the whole turn. It
	// exists to break unbounded stalls in the transport — a dead socket
	// discovered only on the next read, for example — without capping
	// legitimately long tool work such as a delegated sub-agent call or a slow
	// build, both of which run inside the turn but outside the provider call.
	// Zero disables it.
	ModelCallTimeout time.Duration
}
