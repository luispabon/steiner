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
		{Title: "global AGENTS.md"},
		{Title: "project AGENTS.md"},
		{Title: "project context files"},
		{Title: "enabled skills"},
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
	blockMsgIdx, err := reconstructBlockMessageIndex(snapshot.Blocks, len(snapshot.Messages))
	if err != nil {
		return nil, fmt.Errorf("reconstruct block message index: %w", err)
	}
	messageToBlocks := map[int][]int{}
	for blockIdx, msgIdx := range blockMsgIdx {
		messageToBlocks[msgIdx] = append(messageToBlocks[msgIdx], blockIdx)
	}

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

		attributeOrphanMessage(message, categories, index, tokenCount, &conversationOrdinal, &toolOrdinal)
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
func attributeOrphanMessage(message provider.Message, categories []contextReportCategory, index map[string]int, tokenCount int, conversationOrdinal *int, toolOrdinal *int) {
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
	default:
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
}

// assemblyRoleForSource returns the message role used by
// source_render.go::renderBlocks() for merging purposes. This mirrors
// source_render.go::blockMessage() role logic — changes to one must be
// reflected in the other.
func assemblyRoleForSource(source prompt.ContextSource) provider.MessageRole {
	switch source {
	case prompt.ContextSourcePreamble, prompt.ContextSourceGlobalAgentsMD, prompt.ContextSourceProjectAgentsMD, prompt.ContextSourceConversationSummary:
		return provider.MessageRoleSystem
	case prompt.ContextSourceToolSummary, prompt.ContextSourceToolResult, prompt.ContextSourceDelegationResult:
		return provider.MessageRoleTool
	default:
		// ProjectContext, Skill, DurableContext, Conversation
		return provider.MessageRoleUser
	}
}

// reconstructBlockMessageIndex determines which message index each block was merged into.
// This mirrors the merging logic in source_render.go::renderBlocks() — changes to
// one must be reflected in the other.
// Blocks are in assembly order; messages are in the same order with same-role
// blocks merged.
// Pre-conv (non-tool) blocks are mapped by simulating role-based merging.
// Post-conv (tool) blocks each map one-to-one to the final messages.
func reconstructBlockMessageIndex(blocks []prompt.ContextBlock, totalMessages int) ([]int, error) {
	result := make([]int, len(blocks))

	// Find split point: first tool block.
	split := len(blocks)
	for i, block := range blocks {
		if assemblyRoleForSource(block.Source) == provider.MessageRoleTool {
			split = i
			break
		}
	}

	if totalMessages < len(blocks)-split {
		return nil, fmt.Errorf("more post-conv blocks (%d) than messages (%d)", len(blocks)-split, totalMessages)
	}

	// Pre-conv blocks: simulate merging by role transitions.
	msgIdx := 0
	for i := 0; i < split; i++ {
		if i > 0 {
			prevRole := assemblyRoleForSource(blocks[i-1].Source)
			currRole := assemblyRoleForSource(blocks[i].Source)
			if currRole != prevRole {
				msgIdx++
			}
		}
		result[i] = msgIdx
	}

	// Post-conv (tool) blocks: each maps to its own message at the end.
	postConvStart := totalMessages - (len(blocks) - split)
	for j := split; j < len(blocks); j++ {
		result[j] = postConvStart + (j - split)
	}

	return result, nil
}

func classifyBlock(block prompt.ContextBlock) (string, string) {
	switch block.Source {
	case prompt.ContextSourcePreamble:
		return "system preamble", "system preamble"
	case prompt.ContextSourceGlobalAgentsMD:
		return "global AGENTS.md", blockPathLabel(block.Path, "global AGENTS.md")
	case prompt.ContextSourceProjectAgentsMD:
		return "project AGENTS.md", blockPathLabel(block.Path, "project AGENTS.md")
	case prompt.ContextSourceProjectContext:
		return "project context files", blockPathLabel(block.Path, "project context file")
	case prompt.ContextSourceSkill:
		return "enabled skills", skillLabel(block.Path)
	case prompt.ContextSourceDurableContext:
		return "durable context", fallbackLabel(block.Path, "durable context")
	case prompt.ContextSourceConversationSummary:
		return "conversation summary blocks", fallbackLabel(block.Path, "conversation summary")
	case prompt.ContextSourceToolSummary, prompt.ContextSourceToolResult, prompt.ContextSourceDelegationResult:
		return "tool result / tool summary blocks", fallbackLabel(block.Path, "tool summary")
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
