package agent

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
)

// Compactor reduces an oversize conversation to fit the model token budget.
type Compactor interface {
	Compact(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate) (CompactionOutcome, error)
}

// ContextManager is the pipeline hook interface for active context management.
// PostIngestion runs once after the initial conversation is loaded.
// PreAssembly runs before each turn's prompt is assembled.
// OnTurnComplete is called after each turn with whether the scratchpad tool was called.
type ContextManager interface {
	PostIngestion(ctx context.Context, state RunState) (RunState, error)
	PreAssembly(ctx context.Context, state RunState) (RunState, error)
	OnTurnComplete(turnIndex int, scratchpadCalled bool)
}

// MutationRecorder tracks file mutations for context enrichment.
type MutationRecorder interface {
	RecordMutation(path string)
}

// CompactionRecorder records that a compaction event occurred.
type CompactionRecorder interface {
	RecordCompaction(turn int)
}

// EpochResetter resets the masking epoch after compaction.
type EpochResetter interface {
	ResetEpoch(turn int)
}

// EventSinkSetter accepts an event sink for diagnostics emission.
type EventSinkSetter interface {
	SetEventSink(sink output.EventSink)
}

// PreambleProvider returns a cached system preamble string.
type PreambleProvider interface {
	CachedSystemPreamble(override string, scratchpadEnabled bool, delegationEnabled bool) string
}

// ToolResultIngestor processes tool results for context shaping.
type ToolResultIngestor interface {
	ObserveToolResult(turn int, toolName string, input map[string]any, content string) string
}

// AssistantResponseIngestor processes assistant responses for context shaping.
type AssistantResponseIngestor interface {
	IngestAssistantResponse(turn int, content string) (string, string)
}

// ScaffoldInferrer runs scaffold inference to update scratchpad state.
type ScaffoldInferrer interface {
	ShouldRunScaffoldInference(state RunState, compactionCount int) bool
	ApplyScaffoldInference(turn int, content string) (bool, string)
	ScaffoldPromptState() string
}

// CompactionStrategyProvider returns the configured compaction strategy.
type CompactionStrategyProvider interface {
	CompactionStrategy() config.CompactionStrategy
}

// MaskingWindowProvider returns the configured masking window size in turns.
type MaskingWindowProvider interface {
	MaskingWindow() int
}

// NaiveContextManager is a pass-through implementation that leaves state
// unchanged. It preserves the existing compaction behaviour entirely.
type NaiveContextManager struct {
	baseContextManager
}

// PostIngestion normalizes loaded tool output when read annotations are enabled.
func (n *NaiveContextManager) PostIngestion(_ context.Context, state RunState) (RunState, error) {
	next := state.Clone()
	if n.annotationsEnabled() {
		next.Conversation = n.normalizeIngestedMessages(next.TurnCount, next.Conversation, n)
	}
	next.Lineage = newConversationLineage(next.Conversation)
	return next, nil
}

// PreAssembly returns the state unchanged.
func (n *NaiveContextManager) PreAssembly(_ context.Context, state RunState) (RunState, error) {
	return state, nil
}

// OnTurnComplete is a no-op for the naive manager.
func (n *NaiveContextManager) OnTurnComplete(_ int, _ bool) {}

// SetEventSink is a no-op for the naive manager.
func (n *NaiveContextManager) SetEventSink(_ output.EventSink) {}

// ObserveToolResult records heuristic context derived from a tool result.
func (n *NaiveContextManager) ObserveToolResult(turn int, toolName string, input map[string]any, content string) string {
	shaped := n.observeToolResult(turn, toolName, input, content)
	n.fileTracker.ObserveToolResult(turn, toolName, input, shaped)
	return shaped
}

// ResetEpoch prunes tracker state that refers to turns older than the retained conversation.
func (n *NaiveContextManager) ResetEpoch(turn int) {
	n.fileTracker.PruneBeforeTurn(turn)
	n.minVisibleTurn = turn
}

// SmartContextManager applies ingestion-time shaping to tool output so the
// active conversation starts in a compact, signal-rich form.
type SmartContextManager struct {
	baseContextManager
	compactionStrategy config.CompactionStrategy
	scratchpad         ScratchpadManager
	epoch              EpochManager
}

func (s *SmartContextManager) ensureDefaults() {
	if s.annotationsConfigured {
		return
	}
	s.readAnnotations = true
	s.annotationsConfigured = true
}

// CompactionStrategy returns the configured compaction strategy.
func (s *SmartContextManager) CompactionStrategy() config.CompactionStrategy {
	return s.compactionStrategy
}

// MaskingWindow returns the configured masking window in turns, defaulting to 5.
func (s *SmartContextManager) MaskingWindow() int {
	return s.epoch.MaskingWindow()
}

// PostIngestion normalizes tool output in the loaded conversation.
func (s *SmartContextManager) PostIngestion(_ context.Context, state RunState) (RunState, error) {
	s.ensureDefaults()
	next := state.Clone()
	next.Conversation = s.normalizeIngestedMessages(next.TurnCount, next.Conversation, s)
	next.Lineage = newConversationLineage(next.Conversation)
	s.epoch.UpdateMinVisibleTurn(next.Conversation)
	s.minVisibleTurn = s.epoch.MinVisibleTurn()
	s.epoch.InitializeFromTurnCount(next.TurnCount)
	return next, nil
}

// PreAssembly applies non-destructive prompt-time masking on a copy of state.
func (s *SmartContextManager) PreAssembly(_ context.Context, state RunState) (RunState, error) {
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

// IngestAssistantResponse returns the assistant content unchanged.
// Scratchpad state is now captured exclusively via tool results; failure
// counting has moved to OnTurnComplete.
func (s *SmartContextManager) IngestAssistantResponse(_ int, content string) (string, string) {
	return content, ""
}

// OnTurnComplete tracks whether the scratchpad tool was called this turn and
// emits a warning event when three consecutive turns have been missed.
func (s *SmartContextManager) OnTurnComplete(turnIndex int, scratchpadCalled bool) {
	s.scratchpad.OnTurnComplete(turnIndex, scratchpadCalled)
}

// IngestToolResult shapes a newly produced tool result before it enters the
// active conversation history.
func (s *SmartContextManager) IngestToolResult(turn int, toolName, content string) string {
	return s.observeToolResult(turn, toolName, nil, content)
}

// RecordMutation bumps the in-memory file generation for a successful
// steiner-originated mutation.
func (s *SmartContextManager) RecordMutation(path string) {
	s.baseContextManager.RecordMutation(path)
}

// ResetEpoch resets the current masking epoch after compaction so retained
// conversation starts from a clean boundary.
func (s *SmartContextManager) ResetEpoch(turn int) {
	s.fileTracker.PruneBeforeTurn(turn)
	s.minVisibleTurn = turn
	s.epoch.Reset(turn, "compaction")
}

// RecordCompaction appends a scaffold-managed compaction fact.
func (s *SmartContextManager) RecordCompaction(turn int) {
	s.appendDecisionFact(fmt.Sprintf("compaction occurred at turn %d", turn))
}

// ObserveToolResult records heuristic context derived from a tool result.
func (s *SmartContextManager) ObserveToolResult(turn int, toolName string, input map[string]any, content string) string {
	return s.observeToolResult(turn, toolName, input, content)
}

// SetEventSink installs the sink used for context-management diagnostics.
func (s *SmartContextManager) SetEventSink(sink output.EventSink) {
	s.baseContextManager.SetEventSink(sink)
	s.scratchpad.SetEventSink(sink)
	s.epoch.SetEventSink(sink)
}

// NewContextManager constructs the appropriate ContextManager for the given
// mode. An unrecognised mode falls back to NaiveContextManager.
func NewContextManager(mode string, cfg ...config.ContextManagementConfig) ContextManager {
	base := baseContextManager{}
	if len(cfg) > 0 {
		base.readAnnotations = cfg[0].ReadAnnotations
		base.annotationsConfigured = true
	}
	if mode == "smart" {
		if len(cfg) == 0 {
			base.readAnnotations = true
			base.annotationsConfigured = true
		}
		manager := &SmartContextManager{baseContextManager: base}
		manager.scratchpad.mode = config.ScratchpadModeScaffoldOnly
		if len(cfg) > 0 {
			manager.epoch.maskingWindowTurns = cfg[0].MaskingWindowTurns
			manager.compactionStrategy = cfg[0].CompactionStrategy
			if cfg[0].ScratchpadMode != "" {
				manager.scratchpad.mode = cfg[0].ScratchpadMode
			}
		}
		return manager
	}
	return &NaiveContextManager{baseContextManager: base}
}

func (s *SmartContextManager) appendDecisionFact(fact string) {
	s.scratchpad.AppendDecisionFact(fact)
}

func (s *SmartContextManager) enrichContextState(state RunState) ContextState {
	next := state.Context.Clone()
	next.TurnCount = state.TurnCount
	s.scratchpad.SyncState(state, &next)
	next.Scratchpad = s.scratchpad.Render()
	return next
}

// ScaffoldPromptState returns the rendered scratchpad state for scaffold inference.
func (s *SmartContextManager) ScaffoldPromptState() string {
	return s.scratchpad.ScaffoldPromptState()
}

// ShouldRunScaffoldInference reports whether scaffold inference should run for
// the current state and compaction count.
func (s *SmartContextManager) ShouldRunScaffoldInference(state RunState, compactionCount int) bool {
	return s.scratchpad.ShouldRunScaffoldInference(state, compactionCount)
}

// ApplyScaffoldInference parses scaffold inference output and updates the scratchpad.
func (s *SmartContextManager) ApplyScaffoldInference(turn int, content string) (bool, string) {
	return s.scratchpad.ApplyScaffoldInference(turn, content)
}

func shapeIngestedToolResultForContextManager(cm ContextManager, turn int, toolName string, input map[string]any, content string) string {
	if ingestor, ok := cm.(ToolResultIngestor); ok {
		return ingestor.ObserveToolResult(turn, toolName, input, content)
	}
	return content
}

func processAssistantResponseForContextManager(cm ContextManager, turn int, content string) (string, string) {
	if ingestor, ok := cm.(AssistantResponseIngestor); ok {
		return ingestor.IngestAssistantResponse(turn, content)
	}
	return content, ""
}
