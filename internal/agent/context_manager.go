package agent

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
)

const (
	compactionStrategyDrop      = "drop"
	compactionStrategySummarize = "summarize"
	compactionStrategyHybrid    = "hybrid"
	scratchpadModeScaffoldOnly  = "scaffold_only"
	scratchpadModeHybrid        = "hybrid"
)

// Compactor reduces an oversize conversation to fit the model token budget.
type Compactor interface {
	Compact(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate) (CompactionOutcome, error)
}

// ContextStateManager owns the concrete session-local context shaping used by
// the runner.
type ContextStateManager struct {
	baseContextManager
	compactionStrategy string
	scratchpad         ScratchpadManager
	epoch              EpochManager
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
	manager.epoch.maskingWindowTurns = cfg[0].MaskingWindowTurns
	manager.compactionStrategy = cfg[0].CompactionStrategy
	if cfg[0].ScratchpadMode != "" {
		manager.scratchpad.mode = cfg[0].ScratchpadMode
	}
	return manager
}

func (s *ContextStateManager) ensureDefaults() {
	if s.annotationsConfigured {
		return
	}
	s.readAnnotations = true
	s.annotationsConfigured = true
}

// PostIngestion normalizes tool output in the loaded conversation.
func (s *ContextStateManager) PostIngestion(_ context.Context, state RunState) (RunState, error) {
	s.ensureDefaults()
	next := state.Clone()
	next.Conversation = s.normalizeIngestedMessages(next.TurnCount, next.Conversation, s)
	next.Lineage = newConversationLineage(next.Conversation)
	s.epoch.UpdateMinVisibleTurn(next.Conversation)
	s.minVisibleTurn = s.epoch.MinVisibleTurn()
	s.epoch.InitializeFromTurnCount(next.TurnCount)
	return next, nil
}

// PrepareTurnState applies non-destructive prompt-time masking on a copy of state.
func (s *ContextStateManager) PrepareTurnState(_ context.Context, state RunState) (RunState, error) {
	s.ensureDefaults()
	next := state.Clone()
	s.resetTaskStateIfNeeded(&next)
	next.Context = s.enrichContextState(next)
	conversation := next.Lineage.SummaryPrefixStrippedMessages()
	if len(conversation) == 0 {
		conversation = next.Conversation
	}
	s.epoch.InitializeFromTurnCount(next.TurnCount)
	currentTurn := next.TurnCount + 1
	previousBoundary := s.epoch.MaskBoundary()
	previousStartTurn := s.epoch.epochStartTurn
	trigger := ""
	if s.epoch.ShouldAdvance(currentTurn, next) {
		trigger = s.epoch.Advance(currentTurn)
	}
	window := s.epoch.MaskingWindow()
	masked := maskConversationBeforeTurn(conversation, s.epoch.MaskBoundary())
	s.epoch.emitMaskingDiagnostics(currentTurn, window, previousBoundary, previousStartTurn, trigger, conversation, masked)
	next.Conversation = masked
	next.Lineage = next.Lineage.WithCurrentMessages(masked)
	s.epoch.UpdateMinVisibleTurn(masked)
	s.minVisibleTurn = s.epoch.MinVisibleTurn()
	return next, nil
}

// ProcessAssistantResponse returns the assistant content unchanged.
func (s *ContextStateManager) ProcessAssistantResponse(_ int, content string) (string, string) {
	return content, ""
}

// RecordTurnCompletion tracks whether the scratchpad tool was called this turn.
func (s *ContextStateManager) RecordTurnCompletion(turnIndex int, scratchpadCalled bool) {
	s.scratchpad.RecordTurnCompletion(turnIndex, scratchpadCalled)
}

// RecordCompaction appends a scaffold-managed compaction fact.
func (s *ContextStateManager) RecordCompaction(turn int) {
	s.appendDecisionFact(fmt.Sprintf("compaction occurred at turn %d", turn))
}

// ObserveToolResult records heuristic context derived from a tool result.
func (s *ContextStateManager) ObserveToolResult(turn int, toolName string, input map[string]any, content string) string {
	return s.observeToolResult(turn, toolName, input, content)
}

// ResetEpoch resets the current masking epoch after compaction so retained
// conversation starts from a clean boundary.
func (s *ContextStateManager) ResetEpoch(turn int) {
	s.fileTracker.PruneBeforeTurn(turn)
	s.minVisibleTurn = turn
	s.epoch.Reset(turn, "compaction")
}

// SetEventSink installs the sink used for context-management diagnostics.
func (s *ContextStateManager) SetEventSink(sink output.EventSink) {
	s.baseContextManager.SetEventSink(sink)
	s.scratchpad.SetEventSink(sink)
	s.epoch.SetEventSink(sink)
}

func (s *ContextStateManager) appendDecisionFact(fact string) {
	s.scratchpad.AppendDecisionFact(fact)
}

func (s *ContextStateManager) enrichContextState(state RunState) ContextState {
	next := state.Context.Clone()
	next.TurnCount = state.TurnCount
	s.scratchpad.SyncState(state, &next)
	next.Scratchpad = s.scratchpad.Render()
	return next
}

// ScaffoldPromptState returns the rendered scratchpad state for scaffold inference.
func (s *ContextStateManager) ScaffoldPromptState() string {
	return s.scratchpad.ScaffoldPromptState()
}

// ShouldRunScaffoldInference reports whether scaffold inference should run for
// the current state and compaction count.
func (s *ContextStateManager) ShouldRunScaffoldInference(state RunState, compactionCount int) bool {
	return s.scratchpad.ShouldRunScaffoldInference(state, compactionCount)
}

// ApplyScaffoldInference parses scaffold inference output and updates the scratchpad.
func (s *ContextStateManager) ApplyScaffoldInference(turn int, content string) (bool, string) {
	return s.scratchpad.ApplyScaffoldInference(turn, content)
}

func shapeIngestedToolResultForContextManager(cm *ContextStateManager, turn int, toolName string, input map[string]any, content string) string {
	if cm == nil {
		return content
	}
	return cm.ObserveToolResult(turn, toolName, input, content)
}

func processAssistantResponseForContextManager(cm *ContextStateManager, turn int, content string) (string, string) {
	if cm == nil {
		return content, ""
	}
	return cm.ProcessAssistantResponse(turn, content)
}
