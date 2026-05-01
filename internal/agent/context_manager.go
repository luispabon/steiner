package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/tool"
)

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

// SmartContextManager applies ingestion-time shaping to tool output so the
// active conversation starts in a compact, signal-rich form.
type SmartContextManager struct {
	maskingWindowTurns int
	readAnnotations    bool
	configApplied      bool
	fileTracker        FileTracker
	scratchpad         Scratchpad
	scratchpadFailures int
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
	masked := fromProviderMessages(prompt.MaskConversation(toProviderMessages(conversation), s.maskingWindow()))
	next.Conversation = masked
	next.Lineage = next.Lineage.WithCurrentMessages(masked)
	return next, nil
}

// IngestAssistantResponse captures model-written scratchpad state and strips it
// from the visible assistant reply.
func (s *SmartContextManager) IngestAssistantResponse(_ int, content string) (string, string) {
	next, ok := ParseScratchpad(content, s.scratchpad)
	if !ok {
		s.scratchpadFailures++
		note := ""
		if s.scratchpadFailures >= 3 {
			note = "scratchpad block missing or invalid in 3+ consecutive assistant replies"
		}
		return strings.TrimSpace(content), note
	}
	s.scratchpad = next
	s.scratchpadFailures = 0
	return StripScratchpad(content), ""
}

// IngestToolResult shapes a newly produced tool result before it enters the
// active conversation history.
func (s *SmartContextManager) IngestToolResult(turn int, toolName, content string) string {
	shaped := tool.ShapeIngestedToolResult(toolName, content)
	if toolName == "read" {
		return s.fileTracker.ObserveRead(turn, shaped, s.annotationsEnabled())
	}
	return shaped
}

// NewContextManager constructs the appropriate ContextManager for the given
// mode. An unrecognised mode falls back to NaiveContextManager.
func NewContextManager(mode string, cfg ...config.ContextManagementConfig) ContextManager {
	if mode == "smart" {
		manager := &SmartContextManager{}
		if len(cfg) > 0 {
			manager.maskingWindowTurns = cfg[0].MaskingWindowTurns
			manager.readAnnotations = cfg[0].ReadAnnotations
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
