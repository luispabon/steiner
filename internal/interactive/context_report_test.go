package interactive

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestBuildContextReportAgentLabels(t *testing.T) {
	cases := []struct {
		name     string
		snapshot RequestContextSnapshot
		want     string
	}{
		{name: "primary", snapshot: RequestContextSnapshot{}, want: "Agent: primary orchestrator"},
		{name: "typed child", snapshot: RequestContextSnapshot{AgentID: "child-1", AgentType: "code"}, want: "Agent: `code` sub-agent child-1"},
		{name: "untyped child", snapshot: RequestContextSnapshot{AgentID: "child-2"}, want: "Agent: sub-agent child-2"},
		{name: "compaction", snapshot: RequestContextSnapshot{Kind: output.APIRequestKindCompaction}, want: "Agent: primary orchestrator (compaction)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := BuildContextReport(context.Background(), tc.snapshot)
			if err != nil {
				t.Fatalf("BuildContextReport() error = %v", err)
			}
			if !strings.Contains(report, tc.want) {
				t.Fatalf("report missing %q\n%s", tc.want, report)
			}
		})
	}
}

func TestBuildContextReportUsesCapturedPromptTokens(t *testing.T) {
	snapshot := RequestContextSnapshot{
		Model:                 "gpt-4o",
		Messages:              []provider.Message{{Role: provider.MessageRoleUser, Content: "context"}},
		EstimatedPromptTokens: 2000,
		ModelBudget:           prompt.ModelTokenBudget{ContextSize: 4096, MaxCompletionTokens: 128},
	}
	fresh, err := provider.EstimateChatRequestTokens(context.Background(), provider.ChatRequest{
		Model:    snapshot.Model,
		Messages: snapshot.Messages,
	})
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	if fresh == snapshot.EstimatedPromptTokens {
		t.Fatalf("fresh estimate = captured estimate = %d, want differing values", fresh)
	}

	report, err := BuildContextReport(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("BuildContextReport() error = %v", err)
	}
	if !strings.Contains(report, "Prompt tokens: `2000`") {
		t.Fatalf("report = %q, want captured prompt token count", report)
	}
	if !strings.Contains(report, "Prompt occupancy: `2000 / 4096`") {
		t.Fatalf("report = %q, want captured prompt occupancy", report)
	}
	if !strings.Contains(report, "| request framing | 1142 |") {
		t.Fatalf("report = %q, want captured prompt allocation", report)
	}
	if !strings.Contains(report, "Prompt usage: `49%`") {
		t.Fatalf("report = %q, want captured prompt usage", report)
	}
}

func TestBuildContextReportFallsBackToEstimatorForLegacySnapshot(t *testing.T) {
	snapshot := RequestContextSnapshot{
		Model:    "gpt-4o",
		Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: "context"}},
	}
	request := provider.ChatRequest{Model: snapshot.Model, Messages: snapshot.Messages}
	expected, err := provider.EstimateChatRequestTokens(context.Background(), request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}

	report, err := BuildContextReport(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("BuildContextReport() error = %v", err)
	}
	if !strings.Contains(report, fmt.Sprintf("Prompt tokens: `%d`", expected)) {
		t.Fatalf("report = %q, want estimator result %d", report, expected)
	}
}

func TestBuildContextReportUsesCompactionBudget(t *testing.T) {
	snapshot := RequestContextSnapshot{
		Model:     "gpt-4o",
		Messages:  []provider.Message{{Role: provider.MessageRoleUser, Content: "context"}},
		MaxTokens: func() *int { value := 64; return &value }(),
		Kind:      output.APIRequestKindCompaction,
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:            4096,
			MaxCompletionTokens:    128,
			NormalSummaryMaxTokens: 16,
			SafetyMarginTokens:     32,
		},
	}
	report, err := BuildContextReport(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("BuildContextReport() error = %v", err)
	}
	request := provider.ChatRequest{Model: snapshot.Model, Messages: snapshot.Messages, MaxTokens: snapshot.MaxTokens}
	fit, err := snapshot.ModelBudget.FitCompactionRequest(context.Background(), request)
	if err != nil {
		t.Fatalf("FitCompactionRequest() error = %v", err)
	}
	want := fmt.Sprintf("Hard prompt limit: `%d`", fit.HardLimitTokens)
	if !strings.Contains(report, want) {
		t.Fatalf("report missing %q\n%s", want, report)
	}
}

func TestFitReportBudgetUsesRequestKind(t *testing.T) {
	t.Parallel()
	budget := prompt.ModelTokenBudget{
		ContextSize:         4096,
		MaxCompletionTokens: 96,
		SummaryMaxTokens:    8,
		SafetyMarginTokens:  24,
	}
	request := provider.ChatRequest{
		Model:    "gpt-4o",
		Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: "context"}},
	}

	compactionBudget, err := fitReportBudget(context.Background(), budget, output.APIRequestKindCompaction, request)
	if err != nil {
		t.Fatalf("fitReportBudget(compaction) error = %v", err)
	}
	if compactionBudget.ReservedCompletionTokens != 8 {
		t.Fatalf("compaction reserved completion tokens = %d, want 8", compactionBudget.ReservedCompletionTokens)
	}

	normalBudget, err := fitReportBudget(context.Background(), budget, "", request)
	if err != nil {
		t.Fatalf("fitReportBudget(normal) error = %v", err)
	}
	if normalBudget.ReservedCompletionTokens != 96 {
		t.Fatalf("normal reserved completion tokens = %d, want 96", normalBudget.ReservedCompletionTokens)
	}
}

func TestBuildContextReportIncludesCategoriesAndTotals(t *testing.T) {
	maxTokens := 64
	snapshot := RequestContextSnapshot{
		Model: "gpt-4o",
		Messages: []provider.Message{
			{Role: provider.MessageRoleSystem, Content: prompt.SystemPreamble("", false, false, "").Content},
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
			{Source: prompt.ContextSourcePreamble, Content: prompt.SystemPreamble("", false, false, "").Content, ByteSize: len(prompt.SystemPreamble("", false, false, "").Content)},
			{Source: prompt.ContextSourceGlobalAgentsMD, Path: "/tmp/global/AGENTS.md", Content: "global agents", ByteSize: len("global agents")},
			{Source: prompt.ContextSourceProjectAgentsMD, Path: "/tmp/project/AGENTS.md", Content: "project agents", ByteSize: len("project agents")},
			{Source: prompt.ContextSourceProjectContext, Path: "/tmp/project/README.md", Content: "project readme", ByteSize: len("project readme")},
			{Source: prompt.ContextSourceSkill, Path: "/skills/review/SKILL.md", Content: "skill instructions", ByteSize: len("skill instructions")},
			{Source: prompt.ContextSourceDurableContext, Path: "retained context state", Content: `{"kind":"durable_context","content":"focus"}`, ByteSize: len(`{"kind":"durable_context","content":"focus"}`)},
			{Source: prompt.ContextSourceConversationSummary, Content: `{"kind":"conversation_summary","content":"summary"}`, ByteSize: len(`{"kind":"conversation_summary","content":"summary"}`)},
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
		"| Category | Tokens |",
		"| system preamble |",
		"| global AGENTS.md |",
		"| project AGENTS.md |",
		"| project context files |",
		"| enabled skills |",
		"| durable context |",
		"| conversation summary blocks |",
		"| conversation messages |",
		"| tool result / tool summary blocks |",
		"| tool definitions |",
		"## Tool Definitions",
		"| read |",
		"## Context Contents",
		"### preamble",
		"### global_agents_md",
		"### project_agents_md",
		"### project_context",
		"### skill",
		"### durable_context",
		"### conversation_summary",
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

	readTokens, err := provider.EstimateToolSpecTokens(context.Background(), snapshot.Model, snapshot.Tools[0])
	if err != nil {
		t.Fatalf("EstimateToolSpecTokens() error = %v", err)
	}
	if !strings.Contains(report, fmt.Sprintf("| read | %d |", readTokens)) {
		t.Fatalf("report missing per-tool row %q\n%s", fmt.Sprintf("| read | %d |", readTokens), report)
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

func TestBuildContextReportWithMergedMessages(t *testing.T) {
	maxTokens := 64
	preambleBlock := prompt.SystemPreamble("", false, false, "")
	preambleContent := preambleBlock.Content

	// Blocks and messages simulate merged real-assembly output:
	//   - All system blocks (preamble, agents, conversation summary) are
	//     merged into one system message.
	//   - All user blocks (project context, skill, durable context) are
	//     merged into one user message.
	//   - Conversation turns follow, then tool results.
	// Block order matches assembly order: system blocks first, then user
	// blocks, then tool blocks (none here). Conversation summary appears
	// with other system blocks because it belongs to the pre-conv zone.
	snapshot := RequestContextSnapshot{
		Model: "gpt-4o",
		Messages: []provider.Message{
			{Role: provider.MessageRoleSystem, Content: preambleContent + "\n\nglobal agents\n\nproject agents\n\n{\"kind\":\"conversation_summary\",\"content\":\"summary\"}"},
			{Role: provider.MessageRoleUser, Content: "project readme\nskill instructions\n{\"kind\":\"durable_context\",\"content\":\"focus\"}"},
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
			{Source: prompt.ContextSourcePreamble, Content: preambleContent, ByteSize: len(preambleContent)},
			{Source: prompt.ContextSourceGlobalAgentsMD, Path: "/tmp/global/AGENTS.md", Content: "global agents", ByteSize: len("global agents")},
			{Source: prompt.ContextSourceProjectAgentsMD, Path: "/tmp/project/AGENTS.md", Content: "project agents", ByteSize: len("project agents")},
			{Source: prompt.ContextSourceConversationSummary, Content: `{"kind":"conversation_summary","content":"summary"}`, ByteSize: len(`{"kind":"conversation_summary","content":"summary"}`)},
			{Source: prompt.ContextSourceProjectContext, Path: "/tmp/project/README.md", Content: "project readme", ByteSize: len("project readme")},
			{Source: prompt.ContextSourceSkill, Path: "/skills/review/SKILL.md", Content: "skill instructions", ByteSize: len("skill instructions")},
			{Source: prompt.ContextSourceDurableContext, Path: "retained context state", Content: `{"kind":"durable_context","content":"focus"}`, ByteSize: len(`{"kind":"durable_context","content":"focus"}`)},
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

	// All block-source categories must appear.
	for _, want := range []string{
		"| system preamble |",
		"| global AGENTS.md |",
		"| project AGENTS.md |",
		"| project context files |",
		"| enabled skills |",
		"| durable context |",
		"| conversation summary blocks |",
		"| conversation messages |",
		"| tool result / tool summary blocks |",
		"| tool definitions |",
		"## Tool Definitions",
		"| read |",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q\n%s", want, report)
		}
	}

	// Verify conversation messages total equals expected sum of conversation turns
	// (user turn + assistant turn). Block-sourced tokens must not leak into
	// conversation messages.
	userTokens, err := provider.EstimateMessageTokens(context.Background(), "gpt-4o", snapshot.Messages[2])
	if err != nil {
		t.Fatalf("EstimateMessageTokens(user) error = %v", err)
	}
	assistantTokens, err := provider.EstimateMessageTokens(context.Background(), "gpt-4o", snapshot.Messages[3])
	if err != nil {
		t.Fatalf("EstimateMessageTokens(assistant) error = %v", err)
	}
	expectedConvTokens := userTokens + assistantTokens

	convPrefix := "| conversation messages |"
	convIdx := strings.Index(report, convPrefix)
	if convIdx < 0 {
		t.Fatalf("conversation messages category not found in report")
	}
	rest := report[convIdx+len(convPrefix):]
	rest = strings.TrimSpace(rest)
	var convTotal int
	if _, err := fmt.Sscanf(rest, "%d", &convTotal); err != nil {
		t.Fatalf("failed to parse conversation messages total: %v", err)
	}
	if convTotal != expectedConvTokens {
		t.Fatalf("conversation messages total = %d, want %d (sum of user+assistant turns)", convTotal, expectedConvTokens)
	}
}

func TestBuildContextReportOmitsToolDefinitionsWhenNoTools(t *testing.T) {
	snapshot := RequestContextSnapshot{
		Model: "gpt-4o",
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
	if strings.Contains(report, "## Tool Definitions") {
		t.Fatalf("report contains Tool Definitions section with no tools\n%s", report)
	}
	if !strings.Contains(report, "| tool definitions |") {
		t.Fatalf("report missing tool definitions category row\n%s", report)
	}
}
