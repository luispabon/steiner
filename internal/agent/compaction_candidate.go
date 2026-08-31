package agent

import "fmt"

func makeCompactionCandidate(candidate ConversationCandidate, source, retained []Message) ConversationCandidate {
	next := candidate
	if len(source) > 0 {
		next.Messages = cloneMessages(source)
		return next
	}
	next.Messages = cloneMessages(retained)
	return next
}

func selectCompactionCandidate(lineage ConversationLineage, skipped map[string]bool) (ConversationCandidate, bool) {
	candidates := lineage.Candidates()
	if len(candidates) == 0 {
		return ConversationCandidate{}, false
	}

	bestIndex := -1
	for i, candidate := range candidates {
		if len(candidate.Messages) == 0 {
			continue
		}
		if skipped != nil && skipped[compactionCandidateKey(candidate)] {
			continue
		}
		if bestIndex < 0 || richerCandidate(candidate, candidates[bestIndex]) {
			bestIndex = i
		}
	}
	if bestIndex < 0 {
		return ConversationCandidate{}, false
	}
	return candidates[bestIndex], true
}

func richerCandidate(a, b ConversationCandidate) bool {
	if len(a.Messages) != len(b.Messages) {
		return len(a.Messages) > len(b.Messages)
	}
	if a.GenerationID != b.GenerationID {
		return a.GenerationID > b.GenerationID
	}
	if a.View != b.View {
		return a.View == ConversationViewFull
	}
	return false
}

func compactionCandidateKey(candidate ConversationCandidate) string {
	return fmt.Sprintf("%d:%s", candidate.GenerationID, candidate.View)
}

func retainedMessagesForCandidate(lineage ConversationLineage, candidate ConversationCandidate) []Message {
	generation, ok := generationByID(lineage, candidate.GenerationID)
	if !ok {
		return cloneMessages(candidate.Messages)
	}
	if len(generation.SummaryPrefix) > 0 {
		return generation.SummaryPrefixStrippedMessages()
	}
	if candidate.View == ConversationViewSummaryPrefixStripped {
		return cloneMessages(candidate.Messages)
	}
	return nil
}

func generationByID(lineage ConversationLineage, generationID int) (ConversationGeneration, bool) {
	for _, generation := range lineage.Generations {
		if generation.ID == generationID {
			return generation.Clone(), true
		}
	}
	return ConversationGeneration{}, false
}

func compactionRetentionBaseMessages(lineage ConversationLineage, candidate ConversationCandidate) []Message {
	base := retainedMessagesForCandidate(lineage, candidate)
	if len(base) > 0 {
		return base
	}
	return cloneMessages(candidate.Messages)
}

func compactionSourceAndRetention(fullMessages, retentionBase []Message, retainTurns int) ([]Message, []Message) {
	retainedMessages := retainRecentTurns(retentionBase, retainTurns)
	if len(retainedMessages) == 0 {
		return cloneMessages(fullMessages), nil
	}
	if len(retainedMessages) >= len(fullMessages) {
		return nil, cloneMessages(retainedMessages)
	}
	sourceMessages := make([]Message, len(fullMessages)-len(retainedMessages))
	copy(sourceMessages, fullMessages[:len(fullMessages)-len(retainedMessages)])
	return sourceMessages, cloneMessages(retainedMessages)
}

// HasCompactionSource reports whether emergency compaction can separate source
// messages from the most recent retained compaction unit.
func HasCompactionSource(messages []Message) bool {
	sourceMessages, _ := compactionSourceAndRetention(messages, messages, emergencyCompactionRetainTurns)
	return len(sourceMessages) > 0
}

func splitCompactionTurns(messages []Message) [][]Message {
	if len(messages) == 0 {
		return nil
	}

	turns := make([][]Message, 0, len(messages))
	current := make([]Message, 0, len(messages))
	hasAssistant := false
	for _, message := range messages {
		if len(current) > 0 && (message.Role == MessageRoleUser || (message.Role == MessageRoleAssistant && hasAssistant)) {
			turns = append(turns, current)
			current = make([]Message, 0, len(messages)-len(turns))
			hasAssistant = false
		}
		current = append(current, message)
		if message.Role == MessageRoleAssistant {
			hasAssistant = true
		}
	}
	if len(current) > 0 {
		turns = append(turns, current)
	}
	return turns
}

func truncateCompactionMessages(messages []Message, limit int) []Message {
	if len(messages) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 80
	}
	out := make([]Message, 0, len(messages))
	for _, message := range messages {
		cloned := message
		cloned.Content = summarizeTextPreview(cloned.Content, limit)
		out = append(out, cloned)
	}
	return out
}

func retainRecentTurns(messages []Message, retainTurns int) []Message {
	if len(messages) == 0 {
		return nil
	}
	if retainTurns <= 0 {
		retainTurns = normalCompactionRetainTurns
	}

	turns := splitCompactionTurns(messages)
	if len(turns) <= retainTurns {
		return cloneMessages(messages)
	}

	start := len(turns) - retainTurns
	out := make([]Message, 0, len(messages))
	for _, turn := range turns[start:] {
		out = append(out, cloneMessages(turn)...)
	}
	return out
}
