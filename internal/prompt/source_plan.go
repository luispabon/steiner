package prompt

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/skill"
)

type plannedSourceKind string

const (
	plannedSourcePreamble       plannedSourceKind = "preamble"
	plannedSourceAgents         plannedSourceKind = "agents"
	plannedSourceProjectContext plannedSourceKind = "project_context"
	plannedSourceSkills         plannedSourceKind = "skills"
	plannedSourceDurableContext plannedSourceKind = "durable_context"
	plannedSourceConversation   plannedSourceKind = "conversation"
	plannedSourceToolSummaries  plannedSourceKind = "tool_summaries"
)

type plannedSourcePlacement string

const (
	plannedSourcePlacementCore          plannedSourcePlacement = "core"
	plannedSourcePlacementConversation  plannedSourcePlacement = "conversation"
	plannedSourcePlacementToolSummaries plannedSourcePlacement = "tool_summaries"
)

type sourcePlan struct {
	Steps []sourcePlanStep
}

type sourcePlanStep struct {
	Kind        plannedSourceKind
	Placement   plannedSourcePlacement
	PassThrough bool
	Apply       func(context.Context, *assemblyState) error
}

func (a Assembler) planSourceAssembly() sourcePlan {
	opts := a.opts
	policy := a.policy

	return sourcePlan{
		Steps: []sourcePlanStep{
			blockSourceStep(plannedSourcePreamble, plannedSourcePlacementCore, func(_ context.Context, state *assemblyState) error {
				appendPreamble(opts, state)
				return nil
			}),
			blockSourceStep(plannedSourceAgents, plannedSourcePlacementCore, func(_ context.Context, state *assemblyState) error {
				return appendAgents(opts, state)
			}),
			blockSourceStep(plannedSourceProjectContext, plannedSourcePlacementCore, func(_ context.Context, state *assemblyState) error {
				return appendProjectContext(opts, policy, state)
			}),
			blockSourceStep(plannedSourceSkills, plannedSourcePlacementCore, func(ctx context.Context, state *assemblyState) error {
				return appendSkills(ctx, opts, state)
			}),
			blockSourceStep(plannedSourceDurableContext, plannedSourcePlacementCore, func(_ context.Context, state *assemblyState) error {
				appendDurableContext(opts, policy, state)
				return nil
			}),
			passThroughSourceStep(plannedSourceConversation, plannedSourcePlacementConversation, func(_ context.Context, state *assemblyState) error {
				appendConversation(opts, state)
				return nil
			}),
			blockSourceStep(plannedSourceToolSummaries, plannedSourcePlacementToolSummaries, func(_ context.Context, state *assemblyState) error {
				appendToolSummaries(opts, policy, state)
				return nil
			}),
		},
	}
}

func blockSourceStep(kind plannedSourceKind, placement plannedSourcePlacement, apply func(context.Context, *assemblyState) error) sourcePlanStep {
	return sourcePlanStep{
		Kind:        kind,
		Placement:   placement,
		PassThrough: false,
		Apply:       apply,
	}
}

func passThroughSourceStep(kind plannedSourceKind, placement plannedSourcePlacement, apply func(context.Context, *assemblyState) error) sourcePlanStep {
	return sourcePlanStep{
		Kind:        kind,
		Placement:   placement,
		PassThrough: true,
		Apply:       apply,
	}
}

func appendPreamble(opts AssemblyOptions, state *assemblyState) {
	state.appendBlock(SystemPreamble(opts.PromptOverrides.System))
}

func appendAgents(opts AssemblyOptions, state *assemblyState) error {
	globalAgentsPath, projectAgentsPath := agentPaths(opts)

	agentBlocks, err := LoadAgents(globalAgentsPath, projectAgentsPath)
	if err != nil {
		return err
	}
	for _, block := range agentBlocks {
		state.appendBlock(block)
	}
	return nil
}

func appendProjectContext(opts AssemblyOptions, policy AssemblyPolicy, state *assemblyState) error {
	projectContext, err := GatherProjectContext(ProjectContextOptions{
		Root:        opts.ProjectRoot,
		BudgetBytes: policy.Budgets.ProjectContextBytes,
		ExtraFiles:  opts.ProjectContextExtraFiles,
		IgnoreFiles: opts.ProjectContextIgnoreFiles,
	})
	if err != nil {
		return err
	}
	for _, block := range projectContext {
		state.appendBlock(block)
	}
	return nil
}

func appendSkills(ctx context.Context, opts AssemblyOptions, state *assemblyState) error {
	skillRoot := skillRoot(opts)
	skillBlocks, err := LoadSkillBlocks(ctx, skill.Loader{RootDir: skillRoot}, opts.SkillNames)
	if err != nil {
		return err
	}
	for _, block := range skillBlocks {
		state.appendBlock(block)
	}
	return nil
}

func appendDurableContext(opts AssemblyOptions, policy AssemblyPolicy, state *assemblyState) {
	if block, ok := durableContextBlock(opts.ContextState, policy.Compaction); ok {
		state.appendBlock(block)
	}
}

func appendConversation(opts AssemblyOptions, state *assemblyState) {
	for _, message := range opts.Conversation {
		state.appendMessage(message)
	}
}

func appendToolSummaries(opts AssemblyOptions, policy AssemblyPolicy, state *assemblyState) {
	for _, toolResult := range opts.ToolResults {
		block := summarizeToolMessage(toolResult, policy.ToolSummary)
		state.appendBlock(block)
	}
}

func agentPaths(opts AssemblyOptions) (string, string) {
	globalAgentsPath := opts.GlobalAgentsPath
	if globalAgentsPath == "" {
		globalAgentsPath = DefaultGlobalAgentsPath(opts.HomeDir)
	}
	projectAgentsPath := opts.ProjectAgentsPath
	if projectAgentsPath == "" && opts.ProjectRoot != "" {
		projectAgentsPath = filepath.Join(opts.ProjectRoot, "AGENTS.md")
	}
	return globalAgentsPath, projectAgentsPath
}

func skillRoot(opts AssemblyOptions) string {
	skillRoot := opts.SkillsRoot
	if skillRoot == "" {
		skillRoot = DefaultSkillsRoot(opts.HomeDir)
	}
	return skillRoot
}

func durableContextBlock(state DurableContextState, policy CompactionPolicy) (ContextBlock, bool) {
	sections := durableContextSections(state)
	if len(sections) == 0 {
		return ContextBlock{}, false
	}

	joined := strings.Join(sections, "\n")
	limit := policy.SummaryBytes
	if limit <= 0 {
		limit = defaultCompactionSummaryBytes
	}
	content := truncateText(joined, limit)
	if content == "" {
		return ContextBlock{}, false
	}

	envelope := struct {
		Kind      string `json:"kind"`
		Title     string `json:"title"`
		ByteSize  int    `json:"byte_size"`
		Truncated bool   `json:"truncated,omitempty"`
		Content   string `json:"content"`
	}{
		Kind:     "durable_context",
		Title:    "retained context state",
		ByteSize: len(content),
		Content:  content,
	}
	if len(content) < len(joined) {
		envelope.Truncated = true
	}

	encoded := marshalEnvelope(envelope)
	return ContextBlock{
		Source:    ContextSourceDurableContext,
		Path:      envelope.Title,
		Content:   encoded,
		ByteSize:  len(encoded),
		Truncated: envelope.Truncated,
	}, true
}

func durableContextSections(state DurableContextState) []string {
	sections := make([]string, 0, 4)

	if len(state.ActiveConstraints) > 0 {
		lines := make([]string, 0, len(state.ActiveConstraints)+1)
		lines = append(lines, "active constraints:")
		for _, item := range state.ActiveConstraints {
			lines = append(lines, "- "+compactDurableContextEntry(item))
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}

	if len(state.UnresolvedWork) > 0 {
		lines := make([]string, 0, len(state.UnresolvedWork)+1)
		lines = append(lines, "unresolved work:")
		for _, item := range state.UnresolvedWork {
			lines = append(lines, "- "+compactDurableContextEntry(item))
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}

	if state.ActiveFocus != nil && strings.TrimSpace(state.ActiveFocus.Text) != "" {
		sections = append(sections, "active focus:\n- "+compactDurableContextEntry(*state.ActiveFocus))
	}

	if len(state.RetainedSummaries) > 0 {
		lines := make([]string, 0, len(state.RetainedSummaries)+1)
		lines = append(lines, "retained summaries:")
		for _, item := range state.RetainedSummaries {
			lines = append(lines, "- "+compactDurableSummaryEntry(item))
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}

	return sections
}

func compactDurableContextEntry(entry DurableContextEntry) string {
	text := compactMessageContent(entry.Text, 160)
	metadata := make([]string, 0, 2)
	if entry.Source != "" {
		metadata = append(metadata, "source="+entry.Source)
	}
	if entry.Turn > 0 {
		metadata = append(metadata, fmt.Sprintf("turn=%d", entry.Turn))
	}
	if len(metadata) == 0 {
		return text
	}
	return text + " (" + strings.Join(metadata, ", ") + ")"
}

func compactDurableSummaryEntry(entry DurableSummaryEntry) string {
	text := compactMessageContent(entry.Text, 160)
	parts := make([]string, 0, 3)
	if strings.TrimSpace(entry.Title) != "" {
		parts = append(parts, entry.Title)
	}
	parts = append(parts, text)
	metadata := make([]string, 0, 2)
	if entry.Source != "" {
		metadata = append(metadata, "source="+entry.Source)
	}
	if entry.Turn > 0 {
		metadata = append(metadata, fmt.Sprintf("turn=%d", entry.Turn))
	}
	if len(metadata) > 0 {
		parts = append(parts, "("+strings.Join(metadata, ", ")+")")
	}
	return strings.Join(parts, ": ")
}
