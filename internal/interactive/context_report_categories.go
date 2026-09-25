package interactive

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func buildContextCategories(ctx context.Context, snapshot RequestContextSnapshot) ([]contextReportCategory, error) {
	categories := []contextReportCategory{
		{Title: "request framing"},
		{Title: "system preamble"},
		{Title: "oneshot phase prompt"},
		{Title: "global AGENTS.md"},
		{Title: "project AGENTS.md"},
		{Title: "project context files"},
		{Title: "enabled skills"},
		{Title: "session date"},
		{Title: "durable context"},
		{Title: "conversation summary blocks"},
		{Title: "conversation messages"},
		{Title: "tool result / tool summary blocks"},
		{Title: "tool definitions"},
	}
	index := map[string]int{}
	for i, category := range categories {
		index[category.Title] = i
	}

	categories[index["request framing"]].Items = append(categories[index["request framing"]].Items, contextReportItem{
		Label:  "request framing overhead",
		Tokens: provider.RequestOverheadTokens(),
	})
	categories[index["request framing"]].Total += provider.RequestOverheadTokens()

	// Reconstruct block-to-message mapping from assembly order and merging rules.
	blockMsgIdx := reconstructBlockMessageIndex(snapshot.Blocks)
	messageToBlocks := map[int][]int{}
	for blockIdx, msgIdx := range blockMsgIdx {
		messageToBlocks[msgIdx] = append(messageToBlocks[msgIdx], blockIdx)
	}

	// Fold every user message's skill blocks in order to learn which occurrences
	// remain effective, so embedded envelopes in conversation messages can be
	// attributed to the enabled skills category.
	skillActive := latestEffectiveSkills(snapshot.Messages)

	conversationOrdinal := 0
	toolOrdinal := 0
	for msgIdx, message := range snapshot.Messages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tokenCount, err := provider.EstimateMessageTokens(ctx, snapshot.Model, message)
		if err != nil {
			return nil, err
		}

		if blocks, ok := messageToBlocks[msgIdx]; ok && len(blocks) > 0 {
			distributeBlockTokens(snapshot.Blocks, blocks, tokenCount, categories, index)
			continue
		}

		attributeOrphanMessage(message, msgIdx, skillActive, categories, index, tokenCount, &conversationOrdinal, &toolOrdinal)
	}

	for i, tool := range snapshot.Tools {
		tokenCount, err := provider.EstimateToolSpecTokens(ctx, snapshot.Model, tool)
		if err != nil {
			return nil, err
		}
		label := fmt.Sprintf("tool #%d", i+1)
		if name := strings.TrimSpace(tool.Function.Name); name != "" {
			label = name
		}
		categories[index["tool definitions"]].Items = append(categories[index["tool definitions"]].Items, contextReportItem{
			Label:  label,
			Tokens: tokenCount,
		})
		categories[index["tool definitions"]].Total += tokenCount
	}

	return categories, nil
}

// distributeBlockTokens distributes tokenCount proportionally across blocks
// by ByteSize. The first block receives the rounding remainder.
func distributeBlockTokens(allBlocks []prompt.ContextBlock, blockIndices []int, tokenCount int, categories []contextReportCategory, index map[string]int) {
	totalByteSize := 0
	for _, blockIdx := range blockIndices {
		totalByteSize += allBlocks[blockIdx].ByteSize
	}

	if totalByteSize == 0 {
		// No byte-size data: first block gets all tokens, rest get zero-token entries
		// so every block appears in the report.
		for j, blockIdx := range blockIndices {
			tokens := 0
			if j == 0 {
				tokens = tokenCount
			}
			block := allBlocks[blockIdx]
			categoryTitle, label := classifyBlock(block)
			categories[index[categoryTitle]].Items = append(categories[index[categoryTitle]].Items, contextReportItem{
				Label:  label,
				Tokens: tokens,
			})
			categories[index[categoryTitle]].Total += tokens
		}
		return
	}

	// Proportional distribution; first block gets rounding remainder.
	for j, blockIdx := range blockIndices {
		block := allBlocks[blockIdx]
		blockTokens := tokenCount * block.ByteSize / totalByteSize
		if j == 0 {
			// Recalculate remainder rather than using running sum to avoid
			// cascading truncation from integer division.
			remainder := tokenCount
			for k := range blockIndices {
				remainder -= tokenCount * allBlocks[blockIndices[k]].ByteSize / totalByteSize
			}
			blockTokens += remainder
		}

		categoryTitle, label := classifyBlock(block)
		categories[index[categoryTitle]].Items = append(categories[index[categoryTitle]].Items, contextReportItem{
			Label:  label,
			Tokens: blockTokens,
		})
		categories[index[categoryTitle]].Total += blockTokens
	}
}

// attributeOrphanMessage classifies a message that is not mapped to any block
// into either the "tool result / tool summary blocks" or "conversation messages"
// category based on its role, maintaining ordinal counters.
func attributeOrphanMessage(message provider.Message, msgIdx int, skillActive map[skillOccurrence]bool, categories []contextReportCategory, index map[string]int, tokenCount int, conversationOrdinal *int, toolOrdinal *int) {
	switch message.Role {
	case provider.MessageRoleTool:
		*toolOrdinal++
		label := fmt.Sprintf("tool #%d", *toolOrdinal)
		if name := strings.TrimSpace(message.Name); name != "" {
			label += " " + name
		}
		if preview := previewText(message.Content); preview != "" {
			label += ": " + preview
		}
		categories[index["tool result / tool summary blocks"]].Items = append(categories[index["tool result / tool summary blocks"]].Items, contextReportItem{
			Label:  label,
			Tokens: tokenCount,
		})
		categories[index["tool result / tool summary blocks"]].Total += tokenCount
	case provider.MessageRoleUser:
		if _, blocks, rest := prompt.SplitSkillBlocks(message.Content); len(blocks) > 0 {
			attributeUserSkillBlocks(message, msgIdx, blocks, rest, skillActive, categories, index, tokenCount, conversationOrdinal)
			return
		}
		attributeConversationMessage(message, categories, index, tokenCount, conversationOrdinal)
	default:
		attributeConversationMessage(message, categories, index, tokenCount, conversationOrdinal)
	}
}

// attributeConversationMessage attributes a whole message's tokens to the
// conversation messages category, preserving the existing label and preview.
func attributeConversationMessage(message provider.Message, categories []contextReportCategory, index map[string]int, tokenCount int, conversationOrdinal *int) {
	*conversationOrdinal++
	label := fmt.Sprintf("%s #%d", message.Role, *conversationOrdinal)
	if preview := previewText(message.Content); preview != "" {
		label += ": " + preview
	}
	categories[index["conversation messages"]].Items = append(categories[index["conversation messages"]].Items, contextReportItem{
		Label:  label,
		Tokens: tokenCount,
	})
	categories[index["conversation messages"]].Total += tokenCount
}

// staleSkillSuffix marks a skill block that is no longer part of the latest
// effective activation set (a superseded activation or a deactivation).
const staleSkillSuffix = " (inactive, removed at next compaction)"

// skillOccurrence identifies one leading skill block envelope by its message
// index and its position within that message's block run.
type skillOccurrence struct {
	message int
	block   int
}

// latestEffectiveSkills folds skill blocks across the messages in order and
// returns the occurrences still effective. Because a later block for a name
// always supersedes an earlier one, an occurrence is effective only when it is
// the last block for its name and that block is an activation. Ordering, not
// text equality, decides the winner so repeated identical envelopes do not
// collapse onto the wrong occurrence.
func latestEffectiveSkills(messages []provider.Message) map[skillOccurrence]bool {
	last := map[string]skillOccurrence{}
	states := map[skillOccurrence]prompt.SkillBlockState{}
	for msgIdx, message := range messages {
		if message.Role != provider.MessageRoleUser {
			continue
		}
		_, blocks, _ := prompt.SplitSkillBlocks(message.Content)
		for j, block := range blocks {
			occurrence := skillOccurrence{message: msgIdx, block: j}
			last[block.Name] = occurrence
			states[occurrence] = block.State
		}
	}
	active := make(map[skillOccurrence]bool)
	for _, occurrence := range last {
		if states[occurrence] == prompt.SkillBlockActive {
			active[occurrence] = true
		}
	}
	return active
}

// attributeUserSkillBlocks splits a user message's token count across its
// embedded skill envelopes by byte share and attributes the remainder to the
// conversation category. A skill-only message has no conversation item, so its
// rounding remainder stays with the first skill item to keep the message total
// conserved.
func attributeUserSkillBlocks(message provider.Message, msgIdx int, blocks []prompt.SkillBlock, rest string, skillActive map[skillOccurrence]bool, categories []contextReportCategory, index map[string]int, tokenCount int, conversationOrdinal *int) {
	enabledIdx := index["enabled skills"]
	base := len(categories[enabledIdx].Items)
	contentBytes := len(message.Content)
	consumed := 0
	for j, block := range blocks {
		share := 0
		if contentBytes > 0 {
			share = tokenCount * len(block.Text) / contentBytes
		}
		consumed += share
		label := block.Name
		if !skillActive[skillOccurrence{message: msgIdx, block: j}] {
			label += staleSkillSuffix
		}
		categories[enabledIdx].Items = append(categories[enabledIdx].Items, contextReportItem{
			Label:  label,
			Tokens: share,
		})
		categories[enabledIdx].Total += share
	}

	remainder := tokenCount - consumed
	if strings.TrimSpace(rest) != "" {
		*conversationOrdinal++
		label := fmt.Sprintf("%s #%d", message.Role, *conversationOrdinal)
		if preview := previewText(rest); preview != "" {
			label += ": " + preview
		}
		categories[index["conversation messages"]].Items = append(categories[index["conversation messages"]].Items, contextReportItem{
			Label:  label,
			Tokens: remainder,
		})
		categories[index["conversation messages"]].Total += remainder
		return
	}

	if remainder != 0 {
		categories[enabledIdx].Items[base].Tokens += remainder
		categories[enabledIdx].Total += remainder
	}
}

// assemblyRoleForSource returns the message role used by
// source_render.go::renderBlocks() for merging purposes. This mirrors
// source_render.go::blockMessage() role logic — changes to one must be
// reflected in the other.
func assemblyRoleForSource(source prompt.ContextSource) provider.MessageRole {
	switch source {
	case prompt.ContextSourcePreamble, prompt.ContextSourcePhasePrompt, prompt.ContextSourceGlobalAgentsMD, prompt.ContextSourceProjectAgentsMD, prompt.ContextSourceConversationSummary:
		return provider.MessageRoleSystem
	default:
		// ProjectContext, SessionDate, Skill, DurableContext, Conversation
		return provider.MessageRoleUser
	}
}

// reconstructBlockMessageIndex determines which message index each block was merged into.
// This mirrors the merging logic in source_render.go::renderBlocks() — changes to
// one must be reflected in the other.
// Blocks are in assembly order; messages are in the same order with same-role
// blocks merged.
func reconstructBlockMessageIndex(blocks []prompt.ContextBlock) []int {
	result := make([]int, len(blocks))
	msgIdx := 0
	for i, block := range blocks {
		if i > 0 && assemblyRoleForSource(block.Source) != assemblyRoleForSource(blocks[i-1].Source) {
			msgIdx++
		}
		result[i] = msgIdx
	}
	return result
}

// allocateCategoryTokens adjusts the existing category breakdown to the
// captured request estimate while preserving its relative allocation.
func allocateCategoryTokens(categories []contextReportCategory, promptTokens int, captured bool) {
	if !captured {
		return
	}
	currentTotal := 0
	for _, category := range categories {
		currentTotal += category.Total
	}
	if currentTotal <= 0 {
		return
	}

	targets := make([]int, len(categories))
	remaining := promptTokens
	for i, category := range categories {
		targets[i] = category.Total * promptTokens / currentTotal
		if i == len(categories)-1 {
			targets[i] = remaining
		}
		remaining -= targets[i]
	}

	for i := range categories {
		category := &categories[i]
		originalTotal := category.Total
		targetTotal := targets[i]
		if originalTotal <= 0 {
			category.Total = targetTotal
			continue
		}
		itemTargets := make([]int, len(category.Items))
		itemRemaining := targetTotal
		for j, item := range category.Items {
			itemTargets[j] = item.Tokens * targetTotal / originalTotal
			if j == len(category.Items)-1 {
				itemTargets[j] = itemRemaining
			}
			itemRemaining -= itemTargets[j]
		}
		for j := range category.Items {
			category.Items[j].Tokens = itemTargets[j]
		}
		category.Total = targetTotal
	}
}

func classifyBlock(block prompt.ContextBlock) (string, string) {
	switch block.Source {
	case prompt.ContextSourcePreamble:
		return "system preamble", "system preamble"
	case prompt.ContextSourcePhasePrompt:
		return "oneshot phase prompt", "oneshot phase prompt"
	case prompt.ContextSourceGlobalAgentsMD:
		return "global AGENTS.md", blockPathLabel(block.Path, "global AGENTS.md")
	case prompt.ContextSourceProjectAgentsMD:
		return "project AGENTS.md", blockPathLabel(block.Path, "project AGENTS.md")
	case prompt.ContextSourceProjectContext:
		return "project context files", blockPathLabel(block.Path, "project context file")
	case prompt.ContextSourceSkill:
		return "enabled skills", skillLabel(block.Path)
	case prompt.ContextSourceSessionDate:
		return "session date", "session date"
	case prompt.ContextSourceDurableContext:
		return "durable context", fallbackLabel(block.Path, "durable context")
	case prompt.ContextSourceConversationSummary:
		return "conversation summary blocks", fallbackLabel(block.Path, "conversation summary")
	default:
		return "conversation messages", fallbackLabel(block.Path, string(block.Source))
	}
}

func previewText(text string) string {
	return output.TruncateWithEllipsis(text, 48)
}

func blockPathLabel(path, fallback string) string {
	if strings.TrimSpace(path) == "" {
		return fallback
	}
	return path
}

func skillLabel(path string) string {
	if strings.TrimSpace(path) == "" {
		return "skill"
	}
	dir := filepath.Base(filepath.Dir(path))
	base := filepath.Base(path)
	if dir != "." && dir != "" && dir != string(filepath.Separator) {
		return dir + "/" + base
	}
	return base
}

func fallbackLabel(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
