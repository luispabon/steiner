package output

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestBuildContextReportIncludesCategoriesAndTotals(t *testing.T) {
	maxTokens := 64
	snapshot := RequestContextSnapshot{
		Model: "gpt-4o",
		Messages: []provider.Message{
			{Role: provider.MessageRoleSystem, Content: prompt.SystemPreamble("", false, false).Content},
			{Role: provider.MessageRoleSystem, Name: "/tmp/global/AGENTS.md", Content: "global agents"},
			{Role: provider.MessageRoleSystem, Name: "/tmp/project/AGENTS.md", Content: "project agents"},
			{Role: provider.MessageRoleUser, Name: "/tmp/project/README.md", Content: "project readme"},
			{Role: provider.MessageRoleUser, Name: "SKILL.md", Content: "skill instructions"},
			{Role: provider.MessageRoleSystem, Name: "retained context state", Content: `{"kind":"durable_context","content":"focus"}`},
			{Role: provider.MessageRoleSystem, Content: `{"kind":"conversation_summary","content":"summary"}`},
			{Role: provider.MessageRoleUser, Content: "current user message"},
			{Role: provider.MessageRoleAssistant, Content: "assistant reply"},
			{Role: provider.MessageRoleTool, Name: "read", Content: "tool output"},
		},
		Tools: []provider.ToolSpec{
			{
				Type: "function",
				Function: provider.ToolFunctionSpec{
					Name:        "read",
					Description: "Read a file",
					Parameters: map[string]any{
						"type": "object",
					},
				},
			},
		},
		MaxTokens: &maxTokens,
		Blocks: []prompt.ContextBlock{
			{Source: prompt.ContextSourcePreamble, Content: prompt.SystemPreamble("", false, false).Content},
			{Source: prompt.ContextSourceGlobalAgentsMD, Path: "/tmp/global/AGENTS.md", Content: "global agents"},
			{Source: prompt.ContextSourceProjectAgentsMD, Path: "/tmp/project/AGENTS.md", Content: "project agents"},
			{Source: prompt.ContextSourceProjectContext, Path: "/tmp/project/README.md", Content: "project readme"},
			{Source: prompt.ContextSourceSkill, Path: "/skills/review/SKILL.md", Content: "skill instructions"},
			{Source: prompt.ContextSourceDurableContext, Path: "retained context state", Content: `{"kind":"durable_context","content":"focus"}`},
			{Source: prompt.ContextSourceConversationSummary, Content: `{"kind":"conversation_summary","content":"summary"}`},
		},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 128,
			SafetyMarginTokens:  32,
		},
	}

	report, err := BuildContextReport(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("BuildContextReport() error = %v", err)
	}

	for _, want := range []string{
		"Prompt tokens:",
		"Prompt occupancy:",
		"- system preamble:",
		"- global AGENTS.md:",
		"- project AGENTS.md:",
		"- project context files:",
		"- enabled skills:",
		"- durable context:",
		"- conversation summary blocks:",
		"- conversation messages:",
		"- tool result / tool summary blocks:",
		"- tool definitions:",
		"review/SKILL.md",
		"/tmp/project/README.md",
		"read",
		"Compaction threshold: `70%`",
		"Estimator pad: `32`",
		"Hard prompt limit:",
		"Prompt occupancy:",
		"Prompt usage:",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q\n%s", want, report)
		}
	}

	request := provider.ChatRequest{
		Model:     snapshot.Model,
		Messages:  snapshot.Messages,
		Tools:     snapshot.Tools,
		MaxTokens: snapshot.MaxTokens,
	}
	fit, err := snapshot.ModelBudget.FitRequest(context.Background(), request)
	if err != nil {
		t.Fatalf("FitRequest() error = %v", err)
	}
	if !strings.Contains(report, fmt.Sprintf("Prompt occupancy: `%d / 4096`", fit.EstimatedPromptTokens)) {
		t.Fatalf("report = %q, want prompt occupancy %d", report, fit.EstimatedPromptTokens)
	}
	if !strings.Contains(report, fmt.Sprintf("Hard prompt limit: `%d`", fit.HardLimitTokens)) {
		t.Fatalf("report = %q, want hard limit %d", report, fit.HardLimitTokens)
	}
	if !strings.Contains(report, fmt.Sprintf("Prompt usage: `%.0f%%`", fit.PromptUsage*100)) {
		t.Fatalf("report = %q, want prompt usage %.0f%%", report, fit.PromptUsage*100)
	}
}
