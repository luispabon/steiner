package interactive

import "github.com/luispabon/steiner/internal/output"

// cliRuntime controls top-level CLI concerns such as configuration and context
// cancellation. Consumer-defined to avoid coupling to cmd/steiner.
type cliRuntime interface {
	// Model returns the currently configured model name.
	Model() string
}

// runExecutor starts and manages model-in-the-loop runs. Consumer-defined to
// avoid coupling to internal/agent.
type runExecutor interface {
	// ExecuteRun starts a new run with the given prompt.
	ExecuteRun(prompt string) error
}

// approvalCoordinator manages approval workflows for mutation tools.
// Consumer-defined to avoid coupling to internal/agent.
type approvalCoordinator interface {
	// RequestApproval submits an approval request and returns the decision.
	RequestApproval(tool, mode, preview string) (string, error)
}

// snapshotStore provides access to the most recent request context snapshot
// for building context reports. Consumer-defined to avoid coupling to
// internal/agent or internal/prompt.
type snapshotStore interface {
	// LatestSnapshot returns the most recent request context snapshot.
	LatestSnapshot() output.RequestContextSnapshot
}

// Dependencies groups the external dependencies and initial configuration
// required by an interactive session. Each field uses a consumer-defined
// interface to avoid premature coupling to concrete implementations.
type Dependencies struct {
	Runtime             cliRuntime
	Runner              runExecutor
	ApprovalCoordinator approvalCoordinator
	RequestSnapshots    snapshotStore
	DisplaySink         *output.ForwardSink
	SkillNames          []string
}
