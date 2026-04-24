package prompt

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
)

const (
	compactionHeadingRequestIntent       = "Request intent"
	compactionHeadingSolutionDesign      = "Solution design"
	compactionHeadingRecentActions       = "Recent actions"
	compactionHeadingUnresolvedDecisions = "Unresolved decisions"
	compactionHeadingPendingWork         = "Pending work"
	compactionPromptSystemInstruction    = "You compress conversation history for the next model call."
	compactionPromptInstructionBody      = "Output only markdown with these exact headings, in this order:\n# Request intent\n# Solution design\n# Recent actions\n# Unresolved decisions\n# Pending work\nKeep bullets terse. Preserve the user request, the working design, recent actions, unresolved decisions, and pending work. Do not invent new instructions."
)

func BuildConversationCompactionPrompt(messages []provider.Message, state DurableContextState) []provider.Message {
	turns := splitConversationTurns(messages)
	if len(turns) == 0 {
		return nil
	}

	userPrompt := renderConversationCompactionSource(turns, state)
	return []provider.Message{
		{Role: provider.MessageRoleSystem, Content: compactionPromptSystem()},
		{Role: provider.MessageRoleUser, Content: userPrompt},
	}
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

func renderConversationCompactionSource(turns []conversationTurn, state DurableContextState) string {
	sections := []string{
		"conversation:",
		renderConversationTranscript(turns),
	}

	if durable := durableContextSections(state); len(durable) > 0 {
		sections = append(sections,
			"durable context:",
			strings.Join(durable, "\n\n"),
		)
	}

	return strings.Join(filterNonEmptyStrings(sections), "\n\n")
}

func renderConversationTranscript(turns []conversationTurn) string {
	lines := make([]string, 0, len(turns))
	for i, turn := range turns {
		lines = append(lines, "- "+summarizeConversationTurn(i+1, turn))
	}
	return strings.Join(lines, "\n")
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

func compactionPromptSystem() string {
	return compactionPromptSystemInstruction + "\n\n" + compactionPromptInstructionBody
}

func filterNonEmptyStrings(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		filtered = append(filtered, value)
	}
	return filtered
}
