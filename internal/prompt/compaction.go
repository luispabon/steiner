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

func CompactConversationTurns(turns []conversationTurn, policy CompactionPolicy) (SummaryBlock, bool) {
	if len(turns) == 0 {
		return SummaryBlock{}, false
	}

	excerpt, truncated := summarizeConversationTurns(turns, policy.SummaryBytes)
	envelope := ConversationSummaryEnvelope{
		Kind:            "conversation_summary",
		Title:           "compacted conversation history",
		DroppedTurns:    len(turns),
		DroppedMessages: countTurnMessages(turns),
		ByteSize:        len(excerpt),
		Truncated:       truncated,
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

func summarizeConversationTurns(turns []conversationTurn, maxBytes int) (string, bool) {
	if len(turns) == 0 {
		return "", false
	}
	if maxBytes <= 0 {
		maxBytes = defaultCompactionSummaryBytes
	}

	full := renderFixedHeadingConversationSummary(turns)
	truncated := len(full) > maxBytes
	return truncateText(full, maxBytes), truncated
}

func renderFixedHeadingConversationSummary(turns []conversationTurn) string {
	sections := []string{
		renderConversationSummarySection(compactionHeadingRequestIntent, requestIntentBullets(turns)),
		renderConversationSummarySection(compactionHeadingSolutionDesign, solutionDesignBullets(turns)),
		renderConversationSummarySection(compactionHeadingRecentActions, recentActionBullets(turns)),
		renderConversationSummarySection(compactionHeadingUnresolvedDecisions, unresolvedDecisionBullets(turns)),
		renderConversationSummarySection(compactionHeadingPendingWork, pendingWorkBullets(turns)),
	}
	return strings.Join(filterNonEmptyStrings(sections), "\n\n")
}

func renderConversationSummarySection(heading string, bullets []string) string {
	lines := []string{"# " + heading}
	if len(bullets) == 0 {
		lines = append(lines, "- none recorded")
	} else {
		lines = append(lines, bullets...)
	}
	return strings.Join(lines, "\n")
}

func requestIntentBullets(turns []conversationTurn) []string {
	first := firstMessageContent(turns, provider.MessageRoleUser)
	if first == "" {
		return nil
	}
	return []string{"- " + compactMessageContent(first, 160)}
}

func solutionDesignBullets(turns []conversationTurn) []string {
	first := firstMessageContent(turns, provider.MessageRoleAssistant)
	if first == "" {
		return nil
	}
	return []string{"- " + compactMessageContent(first, 160)}
}

func recentActionBullets(turns []conversationTurn) []string {
	if len(turns) == 0 {
		return nil
	}

	start := 0
	if len(turns) > 3 {
		start = len(turns) - 3
	}

	bullets := make([]string, 0, len(turns)-start)
	for i := start; i < len(turns); i++ {
		bullets = append(bullets, "- "+summarizeConversationTurn(i+1, turns[i]))
	}
	return bullets
}

func unresolvedDecisionBullets(turns []conversationTurn) []string {
	bullets := make([]string, 0, len(turns))
	for _, turn := range turns {
		for _, message := range turn.Messages {
			text := strings.ToLower(strings.TrimSpace(message.Content))
			if text == "" {
				continue
			}
			if strings.Contains(text, "?") || strings.Contains(text, "decide") || strings.Contains(text, "choose") || strings.Contains(text, "unsure") {
				bullets = append(bullets, "- "+compactMessageContent(message.Content, 140))
				break
			}
		}
	}
	return bullets
}

func pendingWorkBullets(turns []conversationTurn) []string {
	last := lastMessageContent(turns, provider.MessageRoleAssistant)
	if last == "" {
		last = lastMessageContent(turns, provider.MessageRoleUser)
	}
	if last == "" {
		return nil
	}
	return []string{"- " + compactMessageContent(last, 160)}
}

func firstMessageContent(turns []conversationTurn, role provider.MessageRole) string {
	for _, turn := range turns {
		for _, message := range turn.Messages {
			if message.Role == role && strings.TrimSpace(message.Content) != "" {
				return message.Content
			}
		}
	}
	return ""
}

func lastMessageContent(turns []conversationTurn, role provider.MessageRole) string {
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		for j := len(turn.Messages) - 1; j >= 0; j-- {
			message := turn.Messages[j]
			if message.Role == role && strings.TrimSpace(message.Content) != "" {
				return message.Content
			}
		}
	}
	return ""
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
