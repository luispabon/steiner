package output

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

type RequestContextSnapshot struct {
	Model       string                  `json:"model,omitempty"`
	Messages    []provider.Message      `json:"messages,omitempty"`
	Tools       []provider.ToolSpec     `json:"tools,omitempty"`
	MaxTokens   *int                    `json:"max_tokens,omitempty"`
	Blocks      []prompt.ContextBlock   `json:"blocks,omitempty"`
	ModelBudget prompt.ModelTokenBudget `json:"model_budget,omitempty"`
}

type ContextReportEvent struct {
	Content string `json:"content,omitempty"`
}

type contextReportCategory struct {
	Title string
	Items []contextReportItem
	Total int
}

type contextReportItem struct {
	Label  string
	Tokens int
}

// NewContextReportEvent creates a context report event from the given content.
func NewContextReportEvent(content string) Event {
	return Event{
		Type:      EventTypeContextReport,
		Timestamp: time.Now().UTC(),
		Payload: ContextReportEvent{
			Content: strings.TrimSpace(content),
		},
	}
}

func BuildContextReport(ctx context.Context, snapshot RequestContextSnapshot) (string, error) {
	request := provider.ChatRequest{
		Model:     snapshot.Model,
		Messages:  cloneProviderMessages(snapshot.Messages),
		Tools:     cloneProviderTools(snapshot.Tools),
		MaxTokens: cloneOptionalInt(snapshot.MaxTokens),
	}

	promptTokens, err := provider.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		return "", err
	}
	budget, err := snapshot.ModelBudget.FitRequest(ctx, request)
	if err != nil {
		return "", err
	}

	categories, err := buildContextCategories(ctx, snapshot)
	if err != nil {
		return "", err
	}

	var lines []string
	lines = append(lines, "# Last Request Context")
	if model := strings.TrimSpace(snapshot.Model); model != "" {
		lines = append(lines, fmt.Sprintf("Model: `%s`", model))
	}
	lines = append(lines, fmt.Sprintf("Prompt tokens: `%d`", promptTokens))
	if budget.ContextSize > 0 {
		lines = append(lines, fmt.Sprintf("Prompt occupancy: `%d / %d`", promptTokens, budget.ContextSize))
	} else {
		lines = append(lines, fmt.Sprintf("Prompt occupancy: `%d`", promptTokens))
	}
	lines = append(lines, "")
	lines = append(lines, "## Categories")
	for _, category := range categories {
		lines = append(lines, fmt.Sprintf("- %s: `%d`", category.Title, category.Total))
		for i, item := range category.Items {
			lines = append(lines, fmt.Sprintf("  %d. %s (`%d`)", i+1, item.Label, item.Tokens))
		}
	}
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("Completion reserve: `%d`", budget.ReservedCompletionTokens))
	lines = append(lines, fmt.Sprintf("Safety margin: `%d`", budget.SafetyMarginTokens))
	if budget.ContextSize > 0 {
		lines = append(lines, "Reserve and safety margin are planning buffers, not prompt contents.")
		lines = append(lines, fmt.Sprintf("Budget occupancy: `%d / %d`", budget.TotalTokens, budget.ContextSize))
	} else {
		lines = append(lines, "Reserve and safety margin are planning buffers, not prompt contents.")
		lines = append(lines, fmt.Sprintf("Budget occupancy: `%d`", budget.TotalTokens))
	}
	return strings.Join(lines, "\n"), nil
}

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

	blockMatches := map[string][]prompt.ContextBlock{}
	for _, block := range snapshot.Blocks {
		key := messageMatchKey(blockMessage(block))
		blockMatches[key] = append(blockMatches[key], block)
	}

	conversationOrdinal := 0
	toolOrdinal := 0
	for _, message := range snapshot.Messages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tokenCount, err := provider.EstimateMessageTokens(ctx, snapshot.Model, message)
		if err != nil {
			return nil, err
		}

		if blocks := blockMatches[messageMatchKey(message)]; len(blocks) > 0 {
			block := blocks[0]
			blockMatches[messageMatchKey(message)] = blocks[1:]
			categoryTitle, label := classifyBlock(block)
			categories[index[categoryTitle]].Items = append(categories[index[categoryTitle]].Items, contextReportItem{
				Label:  label,
				Tokens: tokenCount,
			})
			categories[index[categoryTitle]].Total += tokenCount
			continue
		}

		switch message.Role {
		case provider.MessageRoleTool:
			toolOrdinal++
			label := fmt.Sprintf("tool #%d", toolOrdinal)
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
			conversationOrdinal++
			label := fmt.Sprintf("%s #%d", message.Role, conversationOrdinal)
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

func blockMessage(block prompt.ContextBlock) provider.Message {
	message := provider.Message{
		Content: block.Content,
	}
	switch block.Source {
	case prompt.ContextSourcePreamble, prompt.ContextSourceGlobalAgentsMD, prompt.ContextSourceProjectAgentsMD, prompt.ContextSourceConversationSummary, prompt.ContextSourceDurableContext:
		message.Role = provider.MessageRoleSystem
	case prompt.ContextSourceToolSummary, prompt.ContextSourceToolResult, prompt.ContextSourceDelegationResult:
		message.Role = provider.MessageRoleTool
	default:
		message.Role = provider.MessageRoleUser
	}
	if block.Path != "" {
		message.Name = block.Path
	}
	if block.Source == prompt.ContextSourceSkill && block.Path != "" {
		message.Name = filepath.Base(block.Path)
	}
	return message
}

func messageMatchKey(message provider.Message) string {
	var builder strings.Builder
	builder.WriteString(string(message.Role))
	builder.WriteString("\n")
	builder.WriteString(message.Name)
	builder.WriteString("\n")
	builder.WriteString(message.ToolCallID)
	builder.WriteString("\n")
	builder.WriteString(message.Content)
	for _, call := range message.ToolCalls {
		builder.WriteString("\n")
		builder.WriteString(call.ID)
		builder.WriteString("\n")
		builder.WriteString(call.Name)
		builder.WriteString("\n")
		builder.WriteString(fmt.Sprint(call.Arguments))
	}
	return builder.String()
}

func previewText(text string) string {
	return TruncateWithEllipsis(text, 48)
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

func cloneProviderMessages(messages []provider.Message) []provider.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]provider.Message, len(messages))
	for i, message := range messages {
		out[i] = message
		if len(message.ToolCalls) > 0 {
			out[i].ToolCalls = make([]provider.ToolCall, len(message.ToolCalls))
			for j, call := range message.ToolCalls {
				out[i].ToolCalls[j] = call
				if len(call.Arguments) > 0 {
					out[i].ToolCalls[j].Arguments = cloneMap(call.Arguments)
				}
			}
		}
	}
	return out
}

func cloneProviderTools(tools []provider.ToolSpec) []provider.ToolSpec {
	if len(tools) == 0 {
		return nil
	}
	out := make([]provider.ToolSpec, len(tools))
	for i, tool := range tools {
		out[i] = tool
		if len(tool.Function.Parameters) > 0 {
			out[i].Function.Parameters = cloneMap(tool.Function.Parameters)
		}
	}
	return out
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneAny(value)
	}
	return out
}

func cloneAny(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneMap(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = cloneAny(v[i])
		}
		return out
	default:
		return value
	}
}

func cloneOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
