package prompt

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/skill"
)

type Assembler struct {
	opts   AssemblyOptions
	policy AssemblyPolicy
}

func newAssembler(opts AssemblyOptions) (Assembler, error) {
	policy, err := normalizeAssemblyPolicy(opts.Policy)
	if err != nil {
		return Assembler{}, err
	}
	policy.Budgets = policy.Budgets.withProjectContextBudget(opts.ProjectContextBudgetBytes)
	return Assembler{opts: opts, policy: policy}, nil
}

func (a Assembler) Assemble(ctx context.Context) (Assembly, error) {
	blocks := make([]ContextBlock, 0, 8)
	messages := make([]provider.Message, 0, 8+len(a.opts.Conversation)+len(a.opts.ToolResults))
	budgets := newBudgetTracker(a.policy.Budgets)

	appendBlock := func(block ContextBlock) {
		clipped, truncated, ok := applyBudget(budgets, block.Source, block.Content)
		if !ok && len(block.Content) > 0 {
			return
		}
		block.Content = clipped
		block.ByteSize = len(clipped)
		if truncated {
			block.Truncated = true
		}
		blocks = append(blocks, block)
		messages = append(messages, blockMessage(block))
	}

	appendRawMessage := func(source ContextSource, message provider.Message) {
		clipped, _, ok := applyBudget(budgets, source, message.Content)
		if !ok && len(message.Content) > 0 {
			return
		}
		message.Content = clipped
		messages = append(messages, message)
	}

	preamble := SystemPreamble()
	appendBlock(preamble)

	globalAgentsPath := a.opts.GlobalAgentsPath
	if globalAgentsPath == "" {
		globalAgentsPath = DefaultGlobalAgentsPath(a.opts.HomeDir)
	}
	projectAgentsPath := a.opts.ProjectAgentsPath
	if projectAgentsPath == "" && a.opts.ProjectRoot != "" {
		projectAgentsPath = filepath.Join(a.opts.ProjectRoot, "AGENTS.md")
	}

	agentBlocks, err := LoadAgents(globalAgentsPath, projectAgentsPath)
	if err != nil {
		return Assembly{}, err
	}
	for _, block := range agentBlocks {
		appendBlock(block)
	}

	projectContext, err := GatherProjectContext(ProjectContextOptions{
		Root:        a.opts.ProjectRoot,
		BudgetBytes: a.policy.Budgets.ProjectContextBytes,
		ExtraFiles:  a.opts.ProjectContextExtraFiles,
		IgnoreFiles: a.opts.ProjectContextIgnoreFiles,
	})
	if err != nil {
		return Assembly{}, err
	}
	for _, block := range projectContext {
		appendBlock(block)
	}

	skillRoot := a.opts.SkillsRoot
	if skillRoot == "" {
		skillRoot = DefaultSkillsRoot(a.opts.HomeDir)
	}
	skillBlocks, err := LoadSkillBlocks(ctx, skill.Loader{RootDir: skillRoot}, a.opts.SkillNames)
	if err != nil {
		return Assembly{}, err
	}
	for _, block := range skillBlocks {
		appendBlock(block)
	}

	dropped, retained := retainRecentTurns(a.opts.Conversation, a.policy.Retention.RecentTurns)
	if len(dropped) > 0 {
		summary, ok := CompactConversationTurns(dropped, a.policy.Compaction)
		if ok {
			appendBlock(summary.Block())
		}
	}

	for _, turn := range retained {
		for _, message := range turn.Messages {
			appendRawMessage(ContextSourceConversation, message)
		}
	}

	for _, toolResult := range a.opts.ToolResults {
		block := SummarizeToolMessage(toolResult, a.policy.ToolSummary)
		appendBlock(block)
	}

	return Assembly{
		Messages: messages,
		Blocks:   blocks,
	}, nil
}

func applyBudget(tracker *budgetTracker, source ContextSource, content string) (string, bool, bool) {
	if content == "" {
		return "", false, true
	}
	allowed, _ := tracker.take(source, len(content))
	if allowed <= 0 {
		return "", false, false
	}
	if allowed >= len(content) {
		return content, false, true
	}
	return truncateText(content, allowed), true, true
}

func blockMessage(block ContextBlock) provider.Message {
	message := provider.Message{
		Content: block.Content,
	}

	switch block.Source {
	case ContextSourcePreamble, ContextSourceGlobalAgentsMD, ContextSourceProjectAgentsMD:
		message.Role = provider.MessageRoleSystem
	case ContextSourceConversationSummary:
		message.Role = provider.MessageRoleAssistant
	case ContextSourceToolSummary, ContextSourceToolResult, ContextSourceDelegationResult:
		message.Role = provider.MessageRoleTool
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

func (a Assembler) String() string {
	return fmt.Sprintf("Assembler{conversation=%d tool_results=%d}", len(a.opts.Conversation), len(a.opts.ToolResults))
}
