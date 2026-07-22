package prompt

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/skill"
)

type plannedSourceKind string

const (
	plannedSourcePreamble       plannedSourceKind = "preamble"
	plannedSourcePhasePrompt    plannedSourceKind = "phase_prompt"
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
			preambleStep(opts),
			agentsStep(opts),
			projectContextStep(opts, policy),
			skillsStep(opts),
			phasePromptStep(opts),
			conversationStep(opts),
			toolSummariesStep(opts, policy),
		},
	}
}

// appendBlocks appends each block in blocks to state.
func appendBlocks(state *assemblyState, blocks []ContextBlock) {
	for _, block := range blocks {
		state.appendBlock(block)
	}
}

// preambleStep returns the step that loads the system preamble.
// Static sources first — stable prefix maximizes KV cache reuse in local
// inference servers (llama.cpp, LM Studio).
func preambleStep(opts AssemblyOptions) sourcePlanStep {
	return sourcePlanStep{
		Kind:      plannedSourcePreamble,
		Placement: plannedSourcePlacementCore,
		Apply: func(_ context.Context, state *assemblyState) error {
			var block ContextBlock
			if opts.CachedPreamble != "" {
				block = ContextBlock{
					Source:   ContextSourcePreamble,
					Content:  opts.CachedPreamble,
					ByteSize: len(opts.CachedPreamble),
				}
			} else {
				block = systemPreambleWithAdvisor(SystemPreambleParams{
					Override:          opts.PromptOverrides.System,
					DelegationEnabled: opts.DelegationEnabled,
					AdvisorEnabled:    opts.AdvisorEnabled,
					Mode:              opts.WorkflowMode,
					CaveHuman:         opts.CaveHuman,
					SystemSuffix:      opts.PromptOverrides.SystemSuffix,
				})
			}
			// Bypass budget: append directly to blocks and messages so the system
			// preamble is never truncated.
			state.blocks = append(state.blocks, block)
			state.messages = append(state.messages, blockMessage(block))
			return nil
		},
	}
}

// phasePromptStep returns the step that loads the oneshot phase prompt.
// Bypasses budget to ensure phase instructions are always delivered.
func phasePromptStep(opts AssemblyOptions) sourcePlanStep {
	return sourcePlanStep{
		Kind:      plannedSourcePhasePrompt,
		Placement: plannedSourcePlacementCore,
		Apply: func(_ context.Context, state *assemblyState) error {
			content := strings.TrimSpace(opts.PhasePrompt)
			if content == "" {
				return nil
			}
			block := ContextBlock{
				Source:   ContextSourcePhasePrompt,
				Content:  content,
				ByteSize: len(content),
			}
			state.blocks = append(state.blocks, block)
			state.messages = append(state.messages, blockMessage(block))
			return nil
		},
	}
}

// agentsStep returns the step that loads global and project agent definitions.
func agentsStep(opts AssemblyOptions) sourcePlanStep {
	return sourcePlanStep{
		Kind:      plannedSourceAgents,
		Placement: plannedSourcePlacementCore,
		Apply: func(_ context.Context, state *assemblyState) error {
			if opts.SkipProjectContext {
				return nil
			}
			globalAgentsPath, projectAgentsPath := agentPaths(opts)

			agentBlocks, err := loadAgents(globalAgentsPath, projectAgentsPath)
			if err != nil {
				return err
			}
			appendBlocks(state, agentBlocks)
			return nil
		},
	}
}

// projectContextStep returns the step that gathers project context files.
func projectContextStep(opts AssemblyOptions, policy AssemblyPolicy) sourcePlanStep {
	return sourcePlanStep{
		Kind:      plannedSourceProjectContext,
		Placement: plannedSourcePlacementCore,
		Apply: func(_ context.Context, state *assemblyState) error {
			if opts.SkipProjectContext {
				return nil
			}
			projectContext, err := gatherProjectContext(ProjectContextOptions{
				Root:        opts.ProjectRoot,
				BudgetBytes: policy.Budgets.ProjectContextBytes,
				ExtraFiles:  opts.ProjectContextExtraFiles,
				IgnoreFiles: opts.ProjectContextIgnoreFiles,
			})
			if err != nil {
				return err
			}
			appendBlocks(state, projectContext)
			return nil
		},
	}
}

// skillsStep returns the step that loads skill blocks.
func skillsStep(opts AssemblyOptions) sourcePlanStep {
	return sourcePlanStep{
		Kind:      plannedSourceSkills,
		Placement: plannedSourcePlacementCore,
		Apply: func(ctx context.Context, state *assemblyState) error {
			skillRoots := skillRoots(opts)
			skillBlocks, err := loadSkillBlocks(ctx, skill.Loader{RootDirs: skillRoots, BundledFS: opts.SkillsBundledFS}, opts.SkillNames)
			if err != nil {
				return err
			}
			if len(skillBlocks) > 0 {
				skillBlocks = append([]ContextBlock{skillFramingBlock(opts.SkillNames)}, skillBlocks...)
			}
			appendBlocks(state, skillBlocks)
			return nil
		},
	}
}

// conversationStep returns the step that appends conversation messages.
func conversationStep(opts AssemblyOptions) sourcePlanStep {
	return sourcePlanStep{
		Kind:        plannedSourceConversation,
		Placement:   plannedSourcePlacementConversation,
		PassThrough: true,
		Apply: func(_ context.Context, state *assemblyState) error {
			for _, message := range opts.Conversation {
				state.appendMessage(message)
			}
			return nil
		},
	}
}

// toolSummariesStep returns the step that summarizes tool results.
func toolSummariesStep(opts AssemblyOptions, policy AssemblyPolicy) sourcePlanStep {
	return sourcePlanStep{
		Kind:      plannedSourceToolSummaries,
		Placement: plannedSourcePlacementToolSummaries,
		Apply: func(_ context.Context, state *assemblyState) error {
			for _, toolResult := range opts.ToolResults {
				block := summarizeToolMessage(toolResult, policy.ToolSummary)
				state.appendBlock(block)
			}
			return nil
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

func skillRoots(opts AssemblyOptions) []string {
	if len(opts.SkillsRoots) > 0 {
		return opts.SkillsRoots
	}
	return SkillRoots(opts.HomeDir, opts.ProjectRoot)
}
