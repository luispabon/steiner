package agent

import (
	"context"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
)

// Compactor reduces an oversize conversation to fit the model token budget.
type Compactor interface {
	Compact(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate, steering ...string) (CompactionOutcome, error)
}

// ContextStateManager owns the concrete session-local context shaping used by
// the runner.
type ContextStateManager struct {
	baseContextManager
}

// NewContextStateManager builds the concrete context-state manager used by the runner.
func NewContextStateManager(cfg ...config.ContextManagementConfig) *ContextStateManager {
	manager := &ContextStateManager{
		baseContextManager: baseContextManager{
			readAnnotations: true,
		},
	}
	if len(cfg) == 0 {
		return manager
	}
	manager.readAnnotations = cfg[0].ReadAnnotations
	manager.annotationsConfigured = true
	return manager
}

func (s *ContextStateManager) ensureDefaults() {
	if s.annotationsConfigured {
		return
	}
	s.readAnnotations = true
	s.annotationsConfigured = true
}

// PostIngestion restores file-read tracking without changing loaded messages.
func (s *ContextStateManager) PostIngestion(_ context.Context, state RunState) (RunState, error) {
	next := state.Clone()
	for _, message := range next.Conversation {
		if message.Role == MessageRoleTool && message.Name == "read" && message.Turn > 0 {
			s.fileTracker.RecordRead(message.Turn, message.Content)
		}
	}
	next.Lineage = newConversationLineage(next.Conversation)
	return next, nil
}

// PrepareTurnState applies non-destructive context shaping on a copy of state.
func (s *ContextStateManager) PrepareTurnState(_ context.Context, state RunState) (RunState, error) {
	s.ensureDefaults()
	next := state.Clone()
	s.resetTaskStateIfNeeded(&next)
	next.Context = s.enrichContextState(next)
	return next, nil
}

// ProcessAssistantResponse returns the assistant content unchanged.
func (s *ContextStateManager) ProcessAssistantResponse(_ int, content string) (string, string) {
	return content, ""
}

// SetEventSink installs the sink used for context-management diagnostics.
func (s *ContextStateManager) SetEventSink(sink output.EventSink) {
	s.baseContextManager.SetEventSink(sink)
}

func (s *ContextStateManager) enrichContextState(state RunState) ContextState {
	next := state.Context.Clone()
	next.TurnCount = state.TurnCount
	next.CompactionCount = state.Context.CompactionCount
	next.RecentToolCalls = summarizeRecentToolCalls(state.Lineage.latestMessages(), 3)
	return next
}

func shapeFreshToolResultForContextManager(cm *ContextStateManager, turn int, toolName string, input map[string]any, content string, prior []Message) string {
	if cm == nil {
		return content
	}
	return cm.observeFreshToolResult(turn, toolName, input, content, prior)
}

func processAssistantResponseForContextManager(cm *ContextStateManager, turn int, content string) (string, string) {
	if cm == nil {
		return content, ""
	}
	return cm.ProcessAssistantResponse(turn, content)
}
