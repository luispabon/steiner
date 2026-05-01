package agent

import "context"

// Compactor reduces an oversize conversation to fit the model token budget.
type Compactor interface {
	Compact(ctx context.Context, state RunState) (RunState, error)
}

// ContextManager is the pipeline hook interface for active context management.
// PostIngestion runs once after the initial conversation is loaded.
// PreAssembly runs before each turn's prompt is assembled.
type ContextManager interface {
	PostIngestion(ctx context.Context, state RunState) (RunState, error)
	PreAssembly(ctx context.Context, state RunState) (RunState, error)
}

// NaiveContextManager is a pass-through implementation that leaves state
// unchanged. It preserves the existing compaction behaviour entirely.
type NaiveContextManager struct{}

// PostIngestion returns the state unchanged.
func (n *NaiveContextManager) PostIngestion(_ context.Context, state RunState) (RunState, error) {
	return state, nil
}

// PreAssembly returns the state unchanged.
func (n *NaiveContextManager) PreAssembly(_ context.Context, state RunState) (RunState, error) {
	return state, nil
}

// SmartContextManager is the skeleton for active context management. Signal
// extraction and structured state management are wired in later stages;
// currently all methods return the state unchanged.
type SmartContextManager struct{}

// PostIngestion returns the state unchanged until signal extraction is wired.
func (s *SmartContextManager) PostIngestion(_ context.Context, state RunState) (RunState, error) {
	return state, nil
}

// PreAssembly returns the state unchanged until pre-assembly injection is wired.
func (s *SmartContextManager) PreAssembly(_ context.Context, state RunState) (RunState, error) {
	return state, nil
}

// NewContextManager constructs the appropriate ContextManager for the given
// mode. An unrecognised mode falls back to NaiveContextManager.
func NewContextManager(mode string) ContextManager {
	if mode == "smart" {
		return &SmartContextManager{}
	}
	return &NaiveContextManager{}
}
