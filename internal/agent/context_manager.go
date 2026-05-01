package agent

import (
	"context"
	"strings"

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
type SmartContextManager struct{}

// PostIngestion normalizes tool output in the loaded conversation.
func (s *SmartContextManager) PostIngestion(_ context.Context, state RunState) (RunState, error) {
	next := state.Clone()
	next.Conversation = normalizeIngestedMessages(next.Conversation)
	next.Lineage = normalizeIngestedLineage(next.Lineage)
	return next, nil
}

// PreAssembly returns the state unchanged until pre-assembly injection is wired.
func (s *SmartContextManager) PreAssembly(_ context.Context, state RunState) (RunState, error) {
	return state, nil
}

// IngestToolResult shapes a newly produced tool result before it enters the
// active conversation history.
func (s *SmartContextManager) IngestToolResult(toolName, content string) string {
	return tool.ShapeIngestedToolResult(toolName, content)
}

// NewContextManager constructs the appropriate ContextManager for the given
// mode. An unrecognised mode falls back to NaiveContextManager.
func NewContextManager(mode string) ContextManager {
	if mode == "smart" {
		return &SmartContextManager{}
	}
	return &NaiveContextManager{}
}

func normalizeIngestedMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, len(messages))
	for i, message := range messages {
		out[i] = normalizeIngestedMessage(message)
	}
	return out
}

func normalizeIngestedLineage(lineage ConversationLineage) ConversationLineage {
	if len(lineage.Generations) == 0 {
		return lineage.Clone()
	}

	next := lineage.Clone()
	for i := range next.Generations {
		next.Generations[i].SummaryPrefix = normalizeIngestedMessages(next.Generations[i].SummaryPrefix)
		next.Generations[i].Messages = normalizeIngestedMessages(next.Generations[i].Messages)
	}
	return next
}

func normalizeIngestedMessage(message Message) Message {
	if message.Role != MessageRoleTool {
		return message
	}
	if strings.TrimSpace(message.Content) == "" {
		return message
	}
	message.Content = tool.ShapeIngestedToolResult(message.Name, message.Content)
	return message
}

func shapeIngestedToolResultForContextManager(cm ContextManager, toolName, content string) string {
	type toolResultIngestor interface {
		IngestToolResult(toolName, content string) string
	}
	if ingestor, ok := cm.(toolResultIngestor); ok {
		return ingestor.IngestToolResult(toolName, content)
	}
	return content
}
