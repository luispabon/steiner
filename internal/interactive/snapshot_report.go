package interactive

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// RequestContextSnapshot captures the last assembled request for reporting.
type RequestContextSnapshot struct {
	Model                 string                  `json:"model,omitempty"`
	Messages              []provider.Message      `json:"messages,omitempty"`
	Tools                 []provider.ToolSpec     `json:"tools,omitempty"`
	MaxTokens             *int                    `json:"max_tokens,omitempty"`
	Blocks                []prompt.ContextBlock   `json:"blocks,omitempty"`
	ModelBudget           prompt.ModelTokenBudget `json:"model_budget,omitempty"`
	AgentID               string                  `json:"agent_id,omitempty"`
	AgentType             string                  `json:"agent_type,omitempty"`
	Kind                  string                  `json:"kind,omitempty"`
	EstimatedPromptTokens int                     `json:"estimated_prompt_tokens,omitempty"`
	RawPromptTokens       int                     `json:"raw_prompt_tokens,omitempty"`
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

// fitReportBudget selects the fitting method for the request's kind: compaction
// requests fit with the compaction completion reserve so the rendered
// occupancy/limit figures describe the actual request.
func fitReportBudget(ctx context.Context, budget prompt.ModelTokenBudget, kind string, request provider.ChatRequest) (prompt.RequestTokenBudget, error) {
	if kind == output.APIRequestKindCompaction {
		return budget.FitCompactionRequest(ctx, request)
	}
	return budget.FitRequest(ctx, request)
}

func reportPromptUsage(snapshot RequestContextSnapshot, promptTokens int, budget prompt.RequestTokenBudget) float64 {
	if (snapshot.RawPromptTokens > 0 || snapshot.EstimatedPromptTokens > 0) && budget.ContextSize > 0 {
		return float64(promptTokens) / float64(budget.ContextSize)
	}
	return budget.PromptUsage
}

func reportPromptTokens(ctx context.Context, snapshot RequestContextSnapshot, request provider.ChatRequest) (int, bool, error) {
	if snapshot.RawPromptTokens > 0 {
		return snapshot.RawPromptTokens, true, nil
	}
	if snapshot.EstimatedPromptTokens > 0 {
		return snapshot.EstimatedPromptTokens, true, nil
	}
	tokens, err := provider.EstimateChatRequestTokens(ctx, request)
	return tokens, false, err
}

// BuildContextReport summarizes prompt composition and budget usage.
func BuildContextReport(ctx context.Context, snapshot RequestContextSnapshot) (string, error) {
	request := provider.ChatRequest{
		Model:    snapshot.Model,
		Messages: provider.CloneMessages(snapshot.Messages),
		Tools:    provider.CloneTools(snapshot.Tools),
		MaxTokens: func() *int {
			if snapshot.MaxTokens == nil {
				return nil
			}
			cloned := *snapshot.MaxTokens
			return &cloned
		}(),
	}

	promptTokens, captured, err := reportPromptTokens(ctx, snapshot, request)
	if err != nil {
		return "", err
	}
	budget, err := fitReportBudget(ctx, snapshot.ModelBudget, snapshot.Kind, request)
	if err != nil {
		return "", err
	}

	categories, err := buildContextCategories(ctx, snapshot)
	if err != nil {
		return "", err
	}
	allocateCategoryTokens(categories, promptTokens, captured)

	promptUsage := reportPromptUsage(snapshot, promptTokens, budget)
	var lines []string
	lines = append(lines, "# Last Request Context")
	lines = append(lines, fmt.Sprintf("Agent: %s", contextReportAgentLabel(snapshot)))
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
	lines = append(lines, "")
	lines = append(lines, "| Category | Tokens |")
	lines = append(lines, "|----------|--------|")
	for _, category := range categories {
		lines = append(lines, fmt.Sprintf("| %s | %d |", category.Title, category.Total))
	}
	lines = appendToolDefinitions(lines, categories)
	lines = append(lines, "")
	lines = append(lines, "Compaction threshold: `70%`")
	lines = append(lines, fmt.Sprintf("Estimator pad: `%d`", budget.SafetyMarginTokens))
	if budget.ContextSize > 0 {
		lines = append(lines, fmt.Sprintf("Hard prompt limit: `%d`", budget.HardLimitTokens))
		lines = append(lines, fmt.Sprintf("Prompt occupancy: `%d / %d`", promptTokens, budget.ContextSize))
		lines = append(lines, fmt.Sprintf("Prompt usage: `%.0f%%`", promptUsage*100))
	} else {
		lines = append(lines, "Hard prompt limit: `n/a`")
		lines = append(lines, fmt.Sprintf("Prompt occupancy: `%d`", promptTokens))
	}

	// Add context contents section if blocks are present
	if len(snapshot.Blocks) > 0 {
		lines = append(lines, "")
		lines = append(lines, "---")
		lines = append(lines, "## Context Contents")
		lines = append(lines, "")
		for _, block := range snapshot.Blocks {
			lines = append(lines, fmt.Sprintf("### %s", block.Source))
			if block.Path != "" {
				lines = append(lines, fmt.Sprintf("**Path:** `%s`", block.Path))
			}
			lines = append(lines, "")
			lines = append(lines, block.Content)
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n"), nil
}

func contextReportAgentLabel(snapshot RequestContextSnapshot) string {
	label := "primary orchestrator"
	if snapshot.AgentID != "" {
		if snapshot.AgentType != "" {
			label = fmt.Sprintf("`%s` sub-agent %s", snapshot.AgentType, snapshot.AgentID)
		} else {
			label = fmt.Sprintf("sub-agent %s", snapshot.AgentID)
		}
	}
	if snapshot.Kind == output.APIRequestKindCompaction {
		label += " (compaction)"
	}
	return label
}

// appendToolDefinitions appends a per-tool token breakdown for the
// "tool definitions" category when it has items; otherwise it appends nothing.
func appendToolDefinitions(lines []string, categories []contextReportCategory) []string {
	for _, category := range categories {
		if category.Title == "tool definitions" && len(category.Items) > 0 {
			lines = append(lines, "")
			lines = append(lines, "## Tool Definitions")
			lines = append(lines, "")
			lines = append(lines, "| Tool | Tokens |")
			lines = append(lines, "|------|--------|")
			for _, item := range category.Items {
				lines = append(lines, fmt.Sprintf("| %s | %d |", item.Label, item.Tokens))
			}
		}
	}
	return lines
}
