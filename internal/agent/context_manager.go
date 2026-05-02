package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/tool"
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

// RecordMutation is a no-op for the naive manager.
func (n *NaiveContextManager) RecordMutation(_ string) {}

// SmartContextManager applies ingestion-time shaping to tool output so the
// active conversation starts in a compact, signal-rich form.
type SmartContextManager struct {
	maskingWindowTurns     int
	readAnnotations        bool
	configApplied          bool
	compactionStrategy     config.CompactionStrategy
	events                 output.EventSink
	fileTracker            FileTracker
	scratchpad             Scratchpad
	scratchpadFailures     int
	epochMaskBoundary      int
	epochStartTurn         int
	contextPressureTrigger func(currentTurn int, state RunState) bool
	// cachedPreamble holds the system preamble built once per session.
	// Both inputs (override string, scratchpadEnabled bool) are session-constants,
	// so building the string once prevents unnecessary allocations and keeps
	// the bytes byte-identical across turns, preserving KV cache hits.
	cachedPreamble string
}

// CachedSystemPreamble returns the system preamble string, building it once
// and caching it for the lifetime of the manager.
func (s *SmartContextManager) CachedSystemPreamble(override string, scratchpadEnabled bool) string {
	if s.cachedPreamble == "" {
		s.cachedPreamble = prompt.SystemPreamble(override, scratchpadEnabled).Content
	}
	return s.cachedPreamble
}

// Compact selects the configured compaction strategy for smart context
// management.
func (s *SmartContextManager) Compact(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate) (CompactionOutcome, error) {
	switch s.compactionStrategy {
	case config.CompactionStrategyDrop:
		return dropCompactor{retainTurns: defaultDropRetainTurns}.Compact(ctx, req, state, turn, candidate)
	case config.CompactionStrategyHybrid:
		return hybridCompactor{maskingWindowTurns: s.maskingWindow()}.Compact(ctx, req, state, turn, candidate)
	case config.CompactionStrategySummarize, "":
		fallthrough
	default:
		return summarizeCompactor{}.Compact(ctx, req, state, turn, candidate)
	}
}

// PostIngestion normalizes tool output in the loaded conversation.
func (s *SmartContextManager) PostIngestion(_ context.Context, state RunState) (RunState, error) {
	next := state.Clone()
	next.Conversation = s.normalizeIngestedMessages(next.TurnCount, next.Conversation)
	next.Lineage = newConversationLineage(next.Conversation)
	s.initializeEpochFromTurnCount(next.TurnCount)
	return next, nil
}

// PreAssembly applies non-destructive prompt-time masking on a copy of state.
func (s *SmartContextManager) PreAssembly(_ context.Context, state RunState) (RunState, error) {
	next := state.Clone()
	next.Context = s.enrichContextState(next)
	conversation := next.Lineage.SummaryPrefixStrippedMessages()
	if len(conversation) == 0 {
		conversation = next.Conversation
	}
	s.initializeEpochFromTurnCount(next.TurnCount)
	currentTurn := next.TurnCount + 1
	previousBoundary := s.epochMaskBoundary
	previousStartTurn := s.epochStartTurn
	trigger := ""
	if s.shouldAdvanceEpoch(currentTurn, next) {
		trigger = s.advanceEpoch(currentTurn)
	}
	window := s.maskingWindow()
	masked := fromProviderMessages(prompt.MaskConversationBeforeTurn(toProviderMessages(conversation), s.epochMaskBoundary))
	s.emitMaskingDiagnostics(currentTurn, window, previousBoundary, previousStartTurn, trigger, conversation, masked)
	next.Conversation = masked
	next.Lineage = next.Lineage.WithCurrentMessages(masked)
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
	if scratchpadCalled {
		s.scratchpadFailures = 0
		return
	}
	s.scratchpadFailures++
	if s.scratchpadFailures >= 3 {
		note := "scratchpad tool not called in 3+ consecutive turns"
		emitEvent(s.events, output.NewScratchpadEvent(turnIndex, false, s.scratchpad.Render(), s.scratchpadFailures, note))
	}
}

// IngestToolResult shapes a newly produced tool result before it enters the
// active conversation history.
func (s *SmartContextManager) IngestToolResult(turn int, toolName, content string) string {
	shaped := tool.ShapeIngestedToolResult(toolName, content)
	if toolName == "scratchpad" {
		if next, ok := parseScratchpadToolResult(shaped, s.scratchpad); ok {
			s.scratchpad = next
			s.scratchpadFailures = 0
			emitEvent(s.events, output.NewScratchpadEvent(turn, true, s.scratchpad.Render(), 0, ""))
		}
		return `{"ok":true}`
	}
	if toolName == "read" {
		result, _ := parseReadResult(shaped)
		next, observation := s.fileTracker.ObserveRead(turn, shaped, s.annotationsEnabled())
		s.emitFileAnnotationDiagnostics(turn, result, observation, shaped, next)
		return next
	}
	return shaped
}

// RecordMutation bumps the in-memory file generation for a successful
// steiner-originated mutation.
func (s *SmartContextManager) RecordMutation(path string) {
	s.fileTracker.BumpGeneration(path)
}

// ResetEpoch resets the current masking epoch after compaction so retained
// conversation starts from a clean boundary.
func (s *SmartContextManager) ResetEpoch(turn int) {
	s.resetEpoch(turn, "compaction")
}

// SetEventSink installs the sink used for context-management diagnostics.
func (s *SmartContextManager) SetEventSink(sink output.EventSink) {
	s.events = sink
}

// NewContextManager constructs the appropriate ContextManager for the given
// mode. An unrecognised mode falls back to NaiveContextManager.
func NewContextManager(mode string, cfg ...config.ContextManagementConfig) ContextManager {
	if mode == "smart" {
		manager := &SmartContextManager{}
		if len(cfg) > 0 {
			manager.maskingWindowTurns = cfg[0].MaskingWindowTurns
			manager.readAnnotations = cfg[0].ReadAnnotations
			manager.compactionStrategy = cfg[0].CompactionStrategy
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
	message.Content = s.IngestToolResult(turn, message.Name, message.Content)
	return message
}

func (s *SmartContextManager) maskingWindow() int {
	if s.maskingWindowTurns <= 0 {
		return 5
	}
	return s.maskingWindowTurns
}

func (s *SmartContextManager) initializeEpochFromTurnCount(turnCount int) {
	if turnCount <= 0 {
		return
	}
	if s.epochStartTurn > 0 || s.epochMaskBoundary > 0 {
		return
	}
	window := s.maskingWindow()
	s.epochStartTurn = turnCount
	s.epochMaskBoundary = turnCount - window
	if s.epochMaskBoundary < 0 {
		s.epochMaskBoundary = 0
	}
}

func (s *SmartContextManager) shouldAdvanceEpoch(currentTurn int, state RunState) bool {
	window := s.maskingWindow()
	if currentTurn-s.epochStartTurn >= window {
		return true
	}
	if s.contextPressureTrigger == nil {
		return false
	}
	return s.contextPressureTrigger(currentTurn, state)
}

func (s *SmartContextManager) advanceEpoch(currentTurn int) string {
	window := s.maskingWindow()
	trigger := "turn_count"
	if s.contextPressureTrigger != nil && currentTurn-s.epochStartTurn < window {
		trigger = "context_pressure"
	}
	s.epochMaskBoundary = currentTurn - window
	if s.epochMaskBoundary < 0 {
		s.epochMaskBoundary = 0
	}
	s.epochStartTurn = currentTurn
	return trigger
}

func (s *SmartContextManager) resetEpoch(turn int, trigger string) {
	s.epochMaskBoundary = 0
	s.epochStartTurn = turn
	s.emitEpochResetDiagnostic(turn, trigger)
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
	next.Scratchpad = s.scratchpad.Render()
	next.FileTrackerSummary = s.fileTracker.Summaries(5)
	next.RecentToolCalls = summarizeRecentToolCalls(state.Lineage.FullMessages(), 5)
	return next
}

func (s *SmartContextManager) emitFileAnnotationDiagnostics(turn int, result readResult, observation fileObservation, original, shaped string) {
	if s.events == nil {
		return
	}
	if strings.TrimSpace(result.Path) == "" {
		return
	}

	notes := []string{fmt.Sprintf("range=%s", result.rangeSummary())}
	if strings.TrimSpace(original) != strings.TrimSpace(shaped) {
		notes = append(notes, "annotation produced")
	}
	notes = append(notes, observation.Notes...)

	previousTurn := 0
	if observation.HadPrevious {
		previousTurn = observation.PreviousRead.LastTurn
	}
	emitEvent(s.events, output.NewFileAnnotationEvent(
		turn,
		strings.TrimSpace(result.Path),
		observation.Action,
		observation.Reason,
		previousTurn,
		notes...,
	))
}

func (s *SmartContextManager) emitEpochResetDiagnostic(turn int, trigger string) {
	if s.events == nil {
		return
	}
	emitEvent(s.events, output.NewContextMaskingEvent(
		turn,
		"",
		"reset",
		"epoch boundary reset",
		s.maskingWindow(),
		s.epochMaskBoundary,
		s.epochStartTurn,
		0,
		trigger,
		"reset",
	))
}

func (s *SmartContextManager) emitMaskingDiagnostics(turn, window, previousBoundary, previousStartTurn int, trigger string, original, masked []Message) {
	if s.events == nil || len(original) == 0 || len(original) != len(masked) {
		return
	}
	newlyMaskedTurns := map[int]struct{}{}
	for i := range original {
		if strings.TrimSpace(original[i].Content) == strings.TrimSpace(masked[i].Content) {
			continue
		}
		action := "masked"
		reason := "older than masking window"
		epochStatus := "previously_masked"
		toolName := strings.TrimSpace(original[i].Name)
		switch original[i].Role {
		case MessageRoleAssistant:
			if strings.TrimSpace(masked[i].Content) != "" && strings.TrimSpace(masked[i].Content) != strings.TrimSpace(original[i].Content) {
				action = "trimmed"
				reason = "older assistant prose"
			}
		case MessageRoleTool:
			if toolName == "" {
				toolName = "tool"
			}
			reason = "older tool result"
		default:
			action = "masked"
		}
		if trigger != "" && original[i].Turn > 0 && original[i].Turn >= previousBoundary && original[i].Turn < s.epochMaskBoundary {
			epochStatus = "newly_masked"
			newlyMaskedTurns[original[i].Turn] = struct{}{}
		}
		notes := []string{fmt.Sprintf("message_index=%d", i)}
		if original[i].ToolCallID != "" {
			notes = append(notes, "tool_call_id="+original[i].ToolCallID)
		}
		emitEvent(s.events, output.NewContextMaskingEvent(turn, toolName, action, reason, window, s.epochMaskBoundary, s.epochStartTurn, 0, trigger, epochStatus, notes...))
	}
	if trigger != "" {
		emitEvent(s.events, output.NewContextMaskingEvent(
			turn,
			"",
			"advance",
			"epoch boundary advanced",
			window,
			s.epochMaskBoundary,
			s.epochStartTurn,
			len(newlyMaskedTurns),
			trigger,
			"advance",
			fmt.Sprintf("previous_boundary=%d", previousBoundary),
			fmt.Sprintf("previous_start_turn=%d", previousStartTurn),
		))
	}
}

func shapeIngestedToolResultForContextManager(cm ContextManager, turn int, toolName, content string) string {
	type toolResultIngestor interface {
		IngestToolResult(turn int, toolName, content string) string
	}
	if ingestor, ok := cm.(toolResultIngestor); ok {
		return ingestor.IngestToolResult(turn, toolName, content)
	}
	return content
}

func processAssistantResponseForContextManager(cm ContextManager, turn int, content string) (string, string) {
	type assistantIngestor interface {
		IngestAssistantResponse(turn int, content string) (string, string)
	}
	if ingestor, ok := cm.(assistantIngestor); ok {
		return ingestor.IngestAssistantResponse(turn, content)
	}
	return content, ""
}

func summarizeRecentToolCalls(messages []Message, limit int) []string {
	if limit <= 0 {
		return nil
	}
	var out []string
	for i := len(messages) - 1; i >= 0 && len(out) < limit; i-- {
		message := messages[i]
		if message.Role != MessageRoleAssistant || len(message.ToolCalls) == 0 {
			continue
		}
		for j := len(message.ToolCalls) - 1; j >= 0 && len(out) < limit; j-- {
			call := message.ToolCalls[j]
			out = append(out, call.Name+" "+summarizeCallArguments(call.Arguments))
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func summarizeCallArguments(arguments map[string]any) string {
	if len(arguments) == 0 {
		return ""
	}
	parts := make([]string, 0, 3)
	for _, key := range []string{"path", "pattern", "command", "offset", "limit"} {
		value, ok := arguments[key]
		if !ok {
			continue
		}
		parts = append(parts, key+"="+summarizeTextValue(value))
	}
	return strings.TrimSpace(strings.Join(parts, ", "))
}

func summarizeTextValue(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if len(text) <= 40 {
		return text
	}
	return text[:40] + "..."
}

const decisionsMaxBytes = 2000

func parseScratchpadToolResult(content string, previous Scratchpad) (Scratchpad, bool) {
	var fields map[string]string
	if err := json.Unmarshal([]byte(content), &fields); err != nil {
		return previous, false
	}
	next := previous
	if v, ok := fields["goal"]; ok && v != "" {
		next.Goal = v
	}
	if v, ok := fields["plan"]; ok {
		next.Plan = v
	}
	if v, ok := fields["step"]; ok {
		next.Step = v
	}
	if v, ok := fields["next"]; ok {
		next.Next = v
	}
	if v, ok := fields["open"]; ok {
		next.Open = v
	}
	if v, ok := fields["files"]; ok {
		next.Files = v
	}
	// decisions: steiner-managed concatenation with oldest-first eviction at byte cap.
	if v, ok := fields["decisions"]; ok {
		newDecisions := strings.TrimSpace(v)
		if newDecisions != "" && strings.ToLower(newDecisions) != "none" {
			combined := strings.TrimSpace(previous.Decisions)
			if combined != "" {
				combined = combined + "\n" + newDecisions
			} else {
				combined = newDecisions
			}
			// Evict oldest entries (from the start) until under cap.
			for len(combined) > decisionsMaxBytes {
				idx := strings.Index(combined, "\n")
				if idx < 0 {
					// Single entry exceeds cap; truncate from start.
					combined = combined[len(combined)-decisionsMaxBytes:]
					break
				}
				combined = combined[idx+1:]
			}
			next.Decisions = combined
		}
	}
	return next, true
}
