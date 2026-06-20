package agent

// turnOutcome captures the result of advancing one turn.
type turnOutcome struct {
	State RunState
	Stop  bool
	Retry bool
	Error error
}
