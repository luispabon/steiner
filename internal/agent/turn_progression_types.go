package agent

import (
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

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

	// Response carries the model response from executeModelCall to
	// executeToolCalls within a single advance call. Internal use only;
	// never consumed by Runner.Run.
	Response *provider.ChatResponse

	// DetectedReasoningEchoBack is set when the model returned reasoning_content
	// but ReasoningEchoBack was not set on the request. Runner.Run uses this to
	// enable echo-back for subsequent turns.
	DetectedReasoningEchoBack bool
}
