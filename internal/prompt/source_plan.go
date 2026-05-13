package prompt

import (
	"context"
	"path/filepath"

	"github.com/luispabon/steiner/internal/skill"
)

type plannedSourceKind string

const (
	plannedSourcePreamble       plannedSourceKind = "preamble"
	plannedSourceAgents         plannedSourceKind = "agents"
	plannedSourceProjectContext plannedSourceKind = "project_context"
	plannedSourceSkills         plannedSourceKind = "skills"
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

func (a assembler) planSourceAssembly() sourcePlan {
	opts := a.opts
	policy := a.policy

	return sourcePlan{
		Steps: []sourcePlanStep{
			// Static sources first — stable prefix maximizes KV cache
			// reuse in local inference servers (llama.cpp, LM Studio).
			{
				Kind:      plannedSourcePreamble,
				Placement: plannedSourcePlacementCore,
				Apply: func(_ context.Context, state *assemblyState) error {
					if opts.CachedPreamble != "" {
						state.appendBlock(ContextBlock{
							Source:   ContextSourcePreamble,
							Content:  opts.CachedPreamble,
							ByteSize: len(opts.CachedPreamble),
						})
						return nil
					}
					state.appendBlock(SystemPreamble(opts.PromptOverrides.System, opts.ScratchpadEnabled, opts.DelegationEnabled))
					return nil
				},
			},
			{
				Kind:      plannedSourceAgents,
				Placement: plannedSourcePlacementCore,
				Apply: func(_ context.Context, state *assemblyState) error {
					globalAgentsPath, projectAgentsPath := agentPaths(opts)

					agentBlocks, err := loadAgents(globalAgentsPath, projectAgentsPath)
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
					projectContext, err := gatherProjectContext(ProjectContextOptions{
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
					skillBlocks, err := loadSkillBlocks(ctx, skill.Loader{RootDir: skillRoot}, opts.SkillNames)
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
