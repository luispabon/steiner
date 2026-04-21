package prompt

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

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

	if block, ok := durableContextBlock(a.opts.ContextState, a.policy.Compaction); ok {
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
	case ContextSourceDurableContext:
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
