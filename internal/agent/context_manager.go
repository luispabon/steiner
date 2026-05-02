package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

// SmartContextManager applies ingestion-time shaping to tool output so the
// active conversation starts in a compact, signal-rich form.
type SmartContextManager struct {
	maskingWindowTurns int
	readAnnotations    bool
	configApplied      bool
	compactionStrategy config.CompactionStrategy
	events             output.EventSink
	fileTracker        FileTracker
	scratchpad         Scratchpad
	scratchpadFailures int
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
	window := s.maskingWindow()
	masked := fromProviderMessages(prompt.MaskConversation(toProviderMessages(conversation), window))
	s.emitMaskingDiagnostics(next.TurnCount+1, window, conversation, masked)
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
		result, ok := parseReadResult(shaped)
		var previous trackedFileRead
		var hadPrevious bool
		if ok && strings.TrimSpace(result.Path) != "" && s.fileTracker.reads != nil {
			previous, hadPrevious = s.fileTracker.reads[strings.TrimSpace(result.Path)]
		}
		next := s.fileTracker.ObserveRead(turn, shaped, s.annotationsEnabled())
		s.emitFileAnnotationDiagnostics(turn, result, previous, hadPrevious, shaped, next)
		return next
	}
	return shaped
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

func (s *SmartContextManager) emitFileAnnotationDiagnostics(turn int, result readResult, previous trackedFileRead, hadPrevious bool, original, shaped string) {
	if s.events == nil {
		return
	}
	if strings.TrimSpace(result.Path) == "" {
		return
	}

	action := "full"
	reason := "first read"
	if strings.TrimSpace(shaped) != strings.TrimSpace(original) {
		action = "annotated"
		if hadPrevious {
			reason = fmt.Sprintf("unchanged since turn %d", previous.LastTurn)
		} else {
			reason = "unchanged reread"
		}
	} else if !s.annotationsEnabled() {
		reason = "annotations disabled"
	} else if hadPrevious {
		info, err := os.Stat(strings.TrimSpace(result.Path))
		if err == nil && !previous.ModTime.Equal(info.ModTime()) {
			reason = "modified file"
		} else if hadPrevious {
			reason = "served full content"
		}
	}

	notes := []string{fmt.Sprintf("range=%s", result.rangeSummary())}
	if strings.TrimSpace(original) != strings.TrimSpace(shaped) {
		notes = append(notes, "annotation produced")
	}
	if previous.Path != "" {
		notes = append(notes, fmt.Sprintf("previous_turn=%d", previous.LastTurn))
	}
	emitEvent(s.events, output.NewFileAnnotationEvent(turn, strings.TrimSpace(result.Path), action, reason, previous.LastTurn, notes...))
}

func (s *SmartContextManager) emitMaskingDiagnostics(turn, window int, original, masked []Message) {
	if s.events == nil || len(original) == 0 || len(original) != len(masked) {
		return
	}
	for i := range original {
		if strings.TrimSpace(original[i].Content) == strings.TrimSpace(masked[i].Content) {
			continue
		}
		action := "masked"
		reason := "older than masking window"
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
		notes := []string{fmt.Sprintf("message_index=%d", i)}
		if original[i].ToolCallID != "" {
			notes = append(notes, "tool_call_id="+original[i].ToolCallID)
		}
		emitEvent(s.events, output.NewContextMaskingEvent(turn, toolName, action, reason, window, notes...))
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
