package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
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
type NaiveContextManager struct{}

// PostIngestion returns the state unchanged.
func (n *NaiveContextManager) PostIngestion(_ context.Context, state RunState) (RunState, error) {
	return state, nil
}

// PreAssembly returns the state unchanged.
func (n *NaiveContextManager) PreAssembly(_ context.Context, state RunState) (RunState, error) {
	return state, nil
}

// OnTurnComplete is a no-op for the naive manager.
func (n *NaiveContextManager) OnTurnComplete(_ int, _ bool) {}

// SmartContextManager applies ingestion-time shaping to tool output so the
// active conversation starts in a compact, signal-rich form.
type SmartContextManager struct {
	readAnnotations    bool
	configApplied      bool
	compactionStrategy config.CompactionStrategy
	events             output.EventSink
	fileTracker        FileTracker
	scratchpad         ScratchpadManager
	epoch              EpochManager
	// cachedPreamble holds the system preamble built once per session.
	// Both inputs (override string, scratchpadEnabled bool) are session-constants,
	// so building the string once prevents unnecessary allocations and keeps
	// the bytes byte-identical across turns, preserving KV cache hits.
	cachedPreamble string
}

// CachedSystemPreamble returns the system preamble string, building it once
// and caching it for the lifetime of the manager.
func (s *SmartContextManager) CachedSystemPreamble(override string, scratchpadEnabled bool, delegationEnabled bool) string {
	if s.cachedPreamble == "" {
		s.cachedPreamble = prompt.SystemPreamble(override, scratchpadEnabled, delegationEnabled).Content
	}
	return s.cachedPreamble
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
	next := state.Clone()
	next.Conversation = s.normalizeIngestedMessages(next.TurnCount, next.Conversation)
	next.Lineage = newConversationLineage(next.Conversation)
	s.epoch.UpdateMinVisibleTurn(next.Conversation)
	s.epoch.InitializeFromTurnCount(next.TurnCount)
	return next, nil
}

// PreAssembly applies non-destructive prompt-time masking on a copy of state.
func (s *SmartContextManager) PreAssembly(_ context.Context, state RunState) (RunState, error) {
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
	s.fileTracker.RecordMutation(path)
}

// ResetEpoch resets the current masking epoch after compaction so retained
// conversation starts from a clean boundary.
func (s *SmartContextManager) ResetEpoch(turn int) {
	s.fileTracker.PruneBeforeTurn(turn)
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
	s.events = sink
	s.scratchpad.SetEventSink(sink)
	s.epoch.SetEventSink(sink)
}

// NewContextManager constructs the appropriate ContextManager for the given
// mode. An unrecognised mode falls back to NaiveContextManager.
func NewContextManager(mode string, cfg ...config.ContextManagementConfig) ContextManager {
	if mode == "smart" {
		manager := &SmartContextManager{}
		manager.scratchpad.mode = config.ScratchpadModeScaffoldOnly
		if len(cfg) > 0 {
			manager.epoch.maskingWindowTurns = cfg[0].MaskingWindowTurns
			manager.readAnnotations = cfg[0].ReadAnnotations
			manager.compactionStrategy = cfg[0].CompactionStrategy
			if cfg[0].ScratchpadMode != "" {
				manager.scratchpad.mode = cfg[0].ScratchpadMode
			}
			manager.configApplied = true
		}
		return manager
	}
	return &NaiveContextManager{}
}

func (s *SmartContextManager) normalizeIngestedMessages(turn int, messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, len(messages))
	for i, message := range messages {
		out[i] = s.normalizeIngestedMessage(turn, message)
	}
	return out
}

func (s *SmartContextManager) normalizeIngestedMessage(turn int, message Message) Message {
	if message.Role != MessageRoleTool {
		return message
	}
	if strings.TrimSpace(message.Content) == "" {
		return message
	}
	// Loaded tool history should keep each message's recorded turn so distinct
	// reads do not collapse onto one shared session turn.
	messageTurn := message.Turn
	if messageTurn <= 0 {
		messageTurn = turn
	}
	if messageTurn <= 0 {
		return message
	}
	message.Content = s.IngestToolResult(messageTurn, message.Name, message.Content)
	return message
}

func (s *SmartContextManager) appendDecisionFact(fact string) {
	s.scratchpad.AppendDecisionFact(fact)
}

func (s *SmartContextManager) annotationsEnabled() bool {
	if !s.configApplied {
		return true
	}
	return s.readAnnotations
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
