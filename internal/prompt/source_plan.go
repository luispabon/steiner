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
	plannedSourceScratchpad     plannedSourceKind = "scratchpad"
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
			{
				Kind:      plannedSourcePreamble,
				Placement: plannedSourcePlacementCore,
				Apply: func(_ context.Context, state *assemblyState) error {
					state.appendBlock(SystemPreamble(opts.PromptOverrides.System, opts.ScratchpadEnabled))
					return nil
				},
			},
			{
				Kind:      plannedSourceDurableContext,
				Placement: plannedSourcePlacementCore,
				Apply: func(_ context.Context, state *assemblyState) error {
					if block, ok := durableContextBlock(opts.ContextState, policy.Compaction); ok {
						state.appendBlock(block)
					}
					if block, ok := scratchpadBlock(opts.ContextState, opts.ScratchpadEnabled); ok {
						state.appendBlock(block)
					}
					return nil
				},
			},
			{
				Kind:      plannedSourceAgents,
				Placement: plannedSourcePlacementCore,
				Apply: func(_ context.Context, state *assemblyState) error {
					globalAgentsPath, projectAgentsPath := agentPaths(opts)

					agentBlocks, err := LoadAgents(globalAgentsPath, projectAgentsPath)
					if err != nil {
						return err
					}
					for _, block := range agentBlocks {
						state.appendBlock(block)
					}
					return nil
				},
			},
			{
				Kind:      plannedSourceProjectContext,
				Placement: plannedSourcePlacementCore,
				Apply: func(_ context.Context, state *assemblyState) error {
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
				},
			},
			{
				Kind:      plannedSourceSkills,
				Placement: plannedSourcePlacementCore,
				Apply: func(ctx context.Context, state *assemblyState) error {
					skillRoot := skillRoot(opts)
					skillBlocks, err := LoadSkillBlocks(ctx, skill.Loader{RootDir: skillRoot}, opts.SkillNames)
					if err != nil {
						return err
					}
					for _, block := range skillBlocks {
						state.appendBlock(block)
					}
					return nil
				},
			},
			{
				Kind:        plannedSourceConversation,
				Placement:   plannedSourcePlacementConversation,
				PassThrough: true,
				Apply: func(_ context.Context, state *assemblyState) error {
					for _, message := range opts.Conversation {
						state.appendMessage(message)
					}
					return nil
				},
			},
			{
				Kind:      plannedSourceToolSummaries,
				Placement: plannedSourcePlacementToolSummaries,
				Apply: func(_ context.Context, state *assemblyState) error {
					for _, toolResult := range opts.ToolResults {
						block := summarizeToolMessage(toolResult, policy.ToolSummary)
						state.appendBlock(block)
					}
					return nil
				},
			},
		},
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

	if state.TurnCount > 0 || state.CompactionCount > 0 {
		sections = append(sections, compactSessionState(state.TurnCount, state.CompactionCount))
	}

	if len(state.FileTrackerSummary) > 0 {
		sections = append(sections, "tracked files:\n- "+strings.Join(state.FileTrackerSummary, "\n- "))
	}

	if len(state.RecentToolCalls) > 0 {
		sections = append(sections, "recent tool calls:\n- "+strings.Join(state.RecentToolCalls, "\n- "))
	}

	return sections
}

func compactSessionState(turnCount, compactionCount int) string {
	return fmt.Sprintf("session state: turn=%d compactions=%d", turnCount, compactionCount)
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

func scratchpadBlock(state DurableContextState, enabled bool) (ContextBlock, bool) {
	if !enabled {
		return ContextBlock{}, false
	}
	content := strings.TrimSpace(state.Scratchpad)
	if content == "" {
		content = strings.TrimSpace("<scratchpad>\ngoal: \nplan: \nstep: \nnext: \nopen: \n</scratchpad>")
	}
	return ContextBlock{
		Source:   ContextSourceScratchpad,
		Path:     "scratchpad",
		Content:  content,
		ByteSize: len(content),
	}, true
}
