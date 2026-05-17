package output

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// RequestContextSnapshot captures the last assembled request for reporting.
type RequestContextSnapshot struct {
	Model       string                  `json:"model,omitempty"`
	Messages    []provider.Message      `json:"messages,omitempty"`
	Tools       []provider.ToolSpec     `json:"tools,omitempty"`
	MaxTokens   *int                    `json:"max_tokens,omitempty"`
	Blocks      []prompt.ContextBlock   `json:"blocks,omitempty"`
	ModelBudget prompt.ModelTokenBudget `json:"model_budget,omitempty"`
}

// ContextReportEvent carries overlay report content for the TUI.
type ContextReportEvent struct {
	Title   string `json:"title,omitempty"`
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
	return NewOverlayReportEvent("Context Report", content)
}

// NewConfigReportEvent creates a config report event from the given content.
func NewConfigReportEvent(content string) Event {
	return NewOverlayReportEvent("Config", content)
}

// NewOverlayReportEvent creates an overlay report event from the given title and content.
func NewOverlayReportEvent(title, content string) Event {
	return Event{
		Type:      EventTypeContextReport,
		Timestamp: time.Now().UTC(),
		Payload: ContextReportEvent{
			Title:   strings.TrimSpace(title),
			Content: strings.TrimSpace(content),
		},
	}
}

// BuildContextReport summarizes prompt composition and budget usage.
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
	lines = append(lines, "Compaction threshold: `70%`")
	lines = append(lines, fmt.Sprintf("Estimator pad: `%d`", budget.SafetyMarginTokens))
	if budget.ContextSize > 0 {
		lines = append(lines, fmt.Sprintf("Hard prompt limit: `%d`", budget.HardLimitTokens))
		lines = append(lines, fmt.Sprintf("Prompt occupancy: `%d / %d`", promptTokens, budget.ContextSize))
		lines = append(lines, fmt.Sprintf("Prompt usage: `%.0f%%`", budget.PromptUsage*100))
	} else {
		lines = append(lines, "Hard prompt limit: `n/a`")
		lines = append(lines, fmt.Sprintf("Prompt occupancy: `%d`", promptTokens))
	}
	return strings.Join(lines, "\n"), nil
}
