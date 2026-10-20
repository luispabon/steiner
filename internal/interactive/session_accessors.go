package interactive

import (
	"maps"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// EventSink returns the session's composed event sink for external consumers
// to attach to the session's event stream.
func (s *Session) EventSink() output.EventSink {
	return s.events
}

// ProjectRoot returns the session's project root directory.
func (s *Session) ProjectRoot() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deps.WorkDir
}

// Config returns a deep copy of the session's configuration with cloned provider
// and tool configuration maps, so callers can mutate the result without affecting
// the session's live configuration.
func (s *Session) Config() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.deps.Config
	cfg.Providers = maps.Clone(s.deps.Config.Providers)
	cfg.Tools = maps.Clone(s.deps.Config.Tools)
	return cfg
}

// CurrentEffective returns a defensive snapshot of the session's effective model
// assignments.
func (s *Session) CurrentEffective() config.EffectiveModelAssignments {
	s.mu.RLock()
	defer s.mu.RUnlock()

	effective := s.deps.Config.Models.Effective
	effective.SubAgents = cloneStringMap(effective.SubAgents)
	effective.OneShot = cloneStringMap(effective.OneShot)
	effective.WorkflowHandoff = cloneStringMap(effective.WorkflowHandoff)
	return effective
}

// DisplaySink returns the session's ForwardSink, which forwards events to
// whatever target is set via Set. The display_file tool uses this to emit
// display events before the TUI sink is wired in.
func (s *Session) DisplaySink() *output.ForwardSink {
	return s.displaySink
}

// ActiveRunController returns the session's run controller, which manages
// cancellation of the currently active model run.
func (s *Session) ActiveRunController() *ActiveRunController {
	return s.runController
}

// SnapshotStore returns the session's request-context snapshot store.
func (s *Session) SnapshotStore() *SnapshotStore {
	return s.snapshots
}

// ApprovalCoordinator returns the session's approval coordinator, which
// manages pending approval requests.
func (s *Session) ApprovalCoordinator() *ApprovalCoordinator {
	return s.approvalCoordinator
}

// ApprovalHeadIdentity returns the identity of the next FIFO approval.
func (s *Session) ApprovalHeadIdentity() string {
	return s.approvalCoordinator.HeadIdentity()
}

// WorkflowHandoffCoordinator returns the session's workflow handoff
// coordinator, which manages pending handoff requests.
func (s *Session) WorkflowHandoffCoordinator() *WorkflowHandoffCoordinator {
	return s.handoffCoordinator
}

// Approver returns an tool.ApprovalResponder that routes tool approval
// requests through the session's ApprovalCoordinator and emits approval
// events to the given sink.
func (s *Session) Approver(eventSink output.EventSink) tool.ApprovalResponder {
	return newApprovalResponder(s.approvalCoordinator, eventSink)
}

// WorkflowHandoffResponder returns a responder that routes workflow handoff
// decisions through the session's coordinator.
func (s *Session) WorkflowHandoffResponder(eventSink output.EventSink) tool.WorkflowHandoffResponder {
	return newWorkflowHandoffResponder(s.handoffCoordinator, eventSink, s)
}

// CurrentModelAlias returns the currently active model alias.
func (s *Session) CurrentModelAlias() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deps.Config.Models.Effective.ActiveOrchestratorModel
}

// WorkflowHandoffModelSelection returns the configured handoff model for the
// destination when present, otherwise the selected profile's default model alias.
func (s *Session) WorkflowHandoffModelSelection(destination string) WorkflowHandoffModelSelection {
	s.mu.RLock()
	defer s.mu.RUnlock()

	current := strings.TrimSpace(s.deps.Config.Models.Effective.DefaultModel)
	selection := WorkflowHandoffModelSelection{
		ModelAlias:  current,
		SourceLabel: "profile default",
	}

	destination = strings.TrimSpace(destination)
	if destination == "" {
		return selection
	}

	alias := strings.TrimSpace(s.deps.Config.Models.Effective.WorkflowHandoff[destination])
	if alias == "" {
		return selection
	}
	if _, ok := s.deps.Config.Models.Definitions[alias]; !ok {
		return selection
	}

	selection.ModelAlias = alias
	selection.SourceLabel = "from handoff default"
	return selection
}

// CurrentReasoningOverride returns the session's runtime reasoning override
// for the currently active model alias, or the zero value (no override) if
// none has been set for that alias.
func (s *Session) CurrentReasoningOverride() provider.ReasoningOverride {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reasoningOverrides[s.deps.Config.Models.Effective.ActiveOrchestratorModel]
}

// Conversation returns a defensive copy of the current conversation.
func (s *Session) Conversation() []agent.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMessages(s.conversation)
}

// SessionID returns the current session's unique identifier.
func (s *Session) SessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID
}

// PromptCacheKey returns the session's current prompt cache key.
func (s *Session) PromptCacheKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.promptCacheKey
}

// SessionDate returns the date captured for the current session identity.
func (s *Session) SessionDate() prompt.SessionDate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionDate
}

// SessionTitle returns the current session's title, which is empty until
// the first prompt is submitted or a saved session is loaded.
func (s *Session) SessionTitle() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionTitle
}

// Mode returns the current execution mode.
func (s *Session) Mode() config.ExecutionMode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mode
}

// Skills returns the names of all currently enabled skills, in canonical order.
func (s *Session) Skills() []string {
	return s.skills.Snapshot()
}

// OrchestrationLevel returns the current orchestration level.
func (s *Session) OrchestrationLevel() config.OrchestrationLevel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.orchestrationLevel
}
