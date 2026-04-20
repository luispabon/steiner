package prompt

import (
	"context"
	"path/filepath"

	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/skill"
)

func Assemble(ctx context.Context, opts AssemblyOptions) (Assembly, error) {
	if err := validateAssemblyOptions(opts); err != nil {
		return Assembly{}, err
	}

	blocks := make([]ContextBlock, 0, 8)
	messages := make([]provider.Message, 0, 8+len(opts.Conversation)+len(opts.ToolResults))

	preamble := SystemPreamble()
	blocks = append(blocks, preamble)
	messages = append(messages, blockMessage(preamble))

	globalAgentsPath := opts.GlobalAgentsPath
	if globalAgentsPath == "" {
		globalAgentsPath = DefaultGlobalAgentsPath(opts.HomeDir)
	}
	projectAgentsPath := opts.ProjectAgentsPath
	if projectAgentsPath == "" && opts.ProjectRoot != "" {
		projectAgentsPath = filepath.Join(opts.ProjectRoot, "AGENTS.md")
	}

	agentBlocks, err := LoadAgents(globalAgentsPath, projectAgentsPath)
	if err != nil {
		return Assembly{}, err
	}
	for _, block := range agentBlocks {
		blocks = append(blocks, block)
		messages = append(messages, blockMessage(block))
	}

	projectContext, err := GatherProjectContext(ProjectContextOptions{
		Root:        opts.ProjectRoot,
		BudgetBytes: opts.ProjectContextBudgetBytes,
		ExtraFiles:  opts.ProjectContextExtraFiles,
		IgnoreFiles: opts.ProjectContextIgnoreFiles,
	})
	if err != nil {
		return Assembly{}, err
	}
	for _, block := range projectContext {
		blocks = append(blocks, block)
		messages = append(messages, blockMessage(block))
	}

	skillRoot := opts.SkillsRoot
	if skillRoot == "" {
		skillRoot = DefaultSkillsRoot(opts.HomeDir)
	}
	skillBlocks, err := LoadSkillBlocks(ctx, skill.Loader{RootDir: skillRoot}, opts.SkillNames)
	if err != nil {
		return Assembly{}, err
	}
	for _, block := range skillBlocks {
		blocks = append(blocks, block)
		messages = append(messages, blockMessage(block))
	}

	messages = append(messages, opts.Conversation...)
	messages = append(messages, opts.ToolResults...)

	return Assembly{
		Messages: messages,
		Blocks:   blocks,
	}, nil
}

func blockMessage(block ContextBlock) provider.Message {
	message := provider.Message{
		Content: block.Content,
	}

	switch block.Source {
	case ContextSourcePreamble, ContextSourceGlobalAgentsMD, ContextSourceProjectAgentsMD:
		message.Role = provider.MessageRoleSystem
	default:
		message.Role = provider.MessageRoleUser
	}

	if block.Path != "" {
		message.Name = block.Path
	}
	if block.Source == ContextSourceSkill && block.Path != "" {
		message.Name = filepath.Base(block.Path)
	}

	return message
}
