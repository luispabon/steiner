package agent

import "github.com/luispabon/steiner/internal/prompt"

// turnInput carries every value the turn progression needs to run one turn.
type turnInput struct {
	Request           RunRequest
	State             RunState
	BasePrompt        prompt.AssemblyOptions
	CompactionHistory map[string]bool
	CompactionCount   *int
}

// turnOutcome captures the result of advancing one turn.
type turnOutcome struct {
	State RunState
	Stop  bool
	Retry bool
	Error error
}
