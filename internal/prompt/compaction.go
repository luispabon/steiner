package prompt

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
)

type SummaryBlock struct {
	Source          ContextSource
	Title           string
	Content         string
	ByteSize        int
	Truncated       bool
	DroppedTurns    int
	DroppedMessages int
}

func (s SummaryBlock) Block() ContextBlock {
	return ContextBlock{
		Source:    s.Source,
		Path:      s.Title,
		Content:   s.Content,
		ByteSize:  s.ByteSize,
		Truncated: s.Truncated,
	}
}

type ConversationSummaryEnvelope struct {
	Kind            string `json:"kind"`
	Title           string `json:"title"`
	DroppedTurns    int    `json:"dropped_turns"`
	DroppedMessages int    `json:"dropped_messages"`
	ByteSize        int    `json:"byte_size"`
	Truncated       bool   `json:"truncated,omitempty"`
	Content         string `json:"content"`
}

func CompactConversationTurns(turns []conversationTurn, policy CompactionPolicy) (SummaryBlock, bool) {
	if len(turns) == 0 {
		return SummaryBlock{}, false
	}

	excerpt := summarizeConversationTurns(turns, policy.SummaryBytes)
	envelope := ConversationSummaryEnvelope{
		Kind:            "conversation_summary",
		Title:           "compacted conversation history",
		DroppedTurns:    len(turns),
		DroppedMessages: countTurnMessages(turns),
		ByteSize:        len(excerpt),
		Truncated:       len(excerpt) > policy.SummaryBytes,
		Content:         excerpt,
	}
	content := marshalEnvelope(envelope)
	block := SummaryBlock{
		Source:          ContextSourceConversationSummary,
		Title:           envelope.Title,
		Content:         content,
		ByteSize:        len(content),
		Truncated:       envelope.Truncated,
		DroppedTurns:    envelope.DroppedTurns,
		DroppedMessages: envelope.DroppedMessages,
	}
	return block, true
}

func summarizeConversationTurns(turns []conversationTurn, maxBytes int) string {
	if len(turns) == 0 {
		return ""
	}
	if maxBytes <= 0 {
		maxBytes = defaultCompactionSummaryBytes
	}

	parts := make([]string, 0, len(turns))
	for i, turn := range turns {
		parts = append(parts, summarizeConversationTurn(i+1, turn))
	}

	return truncateText(strings.Join(parts, "\n"), maxBytes)
}

func summarizeConversationTurn(index int, turn conversationTurn) string {
	if len(turn.Messages) == 0 {
		return fmt.Sprintf("turn %d: empty", index)
	}

	parts := make([]string, 0, len(turn.Messages))
	for _, message := range turn.Messages {
		parts = append(parts, fmt.Sprintf("%s: %s", message.Role, compactMessageContent(message.Content, 96)))
	}
	return fmt.Sprintf("turn %d: %s", index, strings.Join(parts, " | "))
}

func countTurnMessages(turns []conversationTurn) int {
	total := 0
	for _, turn := range turns {
		total += len(turn.Messages)
	}
	return total
}

type ToolSummaryEnvelope struct {
	Kind      string               `json:"kind"`
	Name      string               `json:"name,omitempty"`
	Role      provider.MessageRole `json:"role,omitempty"`
	ByteSize  int                  `json:"byte_size"`
	Truncated bool                 `json:"truncated,omitempty"`
	Content   string               `json:"content"`
}

func SummarizeToolMessage(message provider.Message, policy ToolSummaryPolicy) ContextBlock {
	limit := policy.MaxBytes
	if limit <= 0 {
		limit = defaultToolSummaryBudgetBytes
	}
	content := truncateText(message.Content, limit)
	envelope := ToolSummaryEnvelope{
		Kind:      "tool_summary",
		Name:      message.Name,
		Role:      provider.MessageRoleTool,
		ByteSize:  len(message.Content),
		Truncated: len(message.Content) > len(content),
		Content:   content,
	}
	encoded := marshalEnvelope(envelope)
	block := ContextBlock{
		Source:    ContextSourceToolSummary,
		Path:      message.Name,
		Content:   encoded,
		ByteSize:  len(encoded),
		Truncated: envelope.Truncated,
	}
	return block
}
