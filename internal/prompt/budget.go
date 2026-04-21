package prompt

import "fmt"

const (
	defaultPreambleBudgetBytes       = 1024
	defaultGlobalAgentsBudgetBytes   = 2048
	defaultProjectAgentsBudgetBytes  = 2048
	defaultSkillBudgetBytes          = 2048
	defaultDurableContextBudgetBytes = 1024
	defaultConversationBudgetBytes   = 4096
	defaultConversationSummaryBytes  = 1024
	defaultToolResultBudgetBytes     = 2048
	defaultToolSummaryBudgetBytes    = 1024
	defaultRecentTurns               = 4
	defaultCompactionSummaryBytes    = 1024
)

func DefaultAssemblyPolicy() AssemblyPolicy {
	return AssemblyPolicy{
		Budgets: SourceBudgetModel{
			PreambleBytes:            defaultPreambleBudgetBytes,
			GlobalAgentsBytes:        defaultGlobalAgentsBudgetBytes,
			ProjectAgentsBytes:       defaultProjectAgentsBudgetBytes,
			ProjectContextBytes:      defaultProjectContextBudgetBytes,
			SkillBytes:               defaultSkillBudgetBytes,
			DurableContextBytes:      defaultDurableContextBudgetBytes,
			ConversationBytes:        defaultConversationBudgetBytes,
			ConversationSummaryBytes: defaultConversationSummaryBytes,
			ToolResultBytes:          defaultToolResultBudgetBytes,
			ToolSummaryBytes:         defaultToolSummaryBudgetBytes,
		},
		Retention:   RetentionPolicy{RecentTurns: defaultRecentTurns},
		Compaction:  CompactionPolicy{SummaryBytes: defaultCompactionSummaryBytes},
		ToolSummary: ToolSummaryPolicy{MaxBytes: defaultToolSummaryBudgetBytes},
	}
}

func normalizeAssemblyPolicy(policy AssemblyPolicy) (AssemblyPolicy, error) {
	defaults := DefaultAssemblyPolicy()

	if policy.Budgets.PreambleBytes < 0 ||
		policy.Budgets.GlobalAgentsBytes < 0 ||
		policy.Budgets.ProjectAgentsBytes < 0 ||
		policy.Budgets.ProjectContextBytes < 0 ||
		policy.Budgets.SkillBytes < 0 ||
		policy.Budgets.DurableContextBytes < 0 ||
		policy.Budgets.ConversationBytes < 0 ||
		policy.Budgets.ConversationSummaryBytes < 0 ||
		policy.Budgets.ToolResultBytes < 0 ||
		policy.Budgets.ToolSummaryBytes < 0 {
		return AssemblyPolicy{}, fmt.Errorf("assembly budgets must not be negative")
	}
	if policy.Retention.RecentTurns < 0 {
		return AssemblyPolicy{}, fmt.Errorf("recent turns must not be negative")
	}
	if policy.Compaction.SummaryBytes < 0 {
		return AssemblyPolicy{}, fmt.Errorf("summary bytes must not be negative")
	}
	if policy.ToolSummary.MaxBytes < 0 {
		return AssemblyPolicy{}, fmt.Errorf("tool summary max bytes must not be negative")
	}

	if policy.Budgets.PreambleBytes == 0 {
		policy.Budgets.PreambleBytes = defaults.Budgets.PreambleBytes
	}
	if policy.Budgets.GlobalAgentsBytes == 0 {
		policy.Budgets.GlobalAgentsBytes = defaults.Budgets.GlobalAgentsBytes
	}
	if policy.Budgets.ProjectAgentsBytes == 0 {
		policy.Budgets.ProjectAgentsBytes = defaults.Budgets.ProjectAgentsBytes
	}
	if policy.Budgets.ProjectContextBytes == 0 {
		policy.Budgets.ProjectContextBytes = defaults.Budgets.ProjectContextBytes
	}
	if policy.Budgets.SkillBytes == 0 {
		policy.Budgets.SkillBytes = defaults.Budgets.SkillBytes
	}
	if policy.Budgets.DurableContextBytes == 0 {
		policy.Budgets.DurableContextBytes = defaults.Budgets.DurableContextBytes
	}
	if policy.Budgets.ConversationBytes == 0 {
		policy.Budgets.ConversationBytes = defaults.Budgets.ConversationBytes
	}
	if policy.Budgets.ConversationSummaryBytes == 0 {
		policy.Budgets.ConversationSummaryBytes = defaults.Budgets.ConversationSummaryBytes
	}
	if policy.Budgets.ToolResultBytes == 0 {
		policy.Budgets.ToolResultBytes = defaults.Budgets.ToolResultBytes
	}
	if policy.Budgets.ToolSummaryBytes == 0 {
		policy.Budgets.ToolSummaryBytes = defaults.Budgets.ToolSummaryBytes
	}

	if policy.Retention.RecentTurns == 0 {
		policy.Retention.RecentTurns = defaults.Retention.RecentTurns
	}
	if policy.Compaction.SummaryBytes == 0 {
		policy.Compaction.SummaryBytes = defaults.Compaction.SummaryBytes
	}
	if policy.ToolSummary.MaxBytes == 0 {
		policy.ToolSummary.MaxBytes = defaults.ToolSummary.MaxBytes
	}
	return policy, nil
}

func validateAssemblyOptions(opts AssemblyOptions) error {
	if opts.ProjectContextBudgetBytes < 0 {
		return fmt.Errorf("project context budget must not be negative")
	}
	return nil
}

func (m SourceBudgetModel) limitFor(source ContextSource) int {
	switch source {
	case ContextSourcePreamble:
		return m.PreambleBytes
	case ContextSourceGlobalAgentsMD:
		return m.GlobalAgentsBytes
	case ContextSourceProjectAgentsMD:
		return m.ProjectAgentsBytes
	case ContextSourceProjectContext:
		return m.ProjectContextBytes
	case ContextSourceSkill:
		return m.SkillBytes
	case ContextSourceDurableContext:
		return m.DurableContextBytes
	case ContextSourceConversationSummary:
		return m.ConversationSummaryBytes
	case ContextSourceToolSummary, ContextSourceDelegationResult:
		return m.ToolSummaryBytes
	case ContextSourceToolResult:
		return m.ToolResultBytes
	case ContextSourceConversation:
		return m.ConversationBytes
	default:
		return m.ConversationBytes
	}
}

func (m SourceBudgetModel) withProjectContextBudget(bytes int) SourceBudgetModel {
	if bytes > 0 {
		m.ProjectContextBytes = bytes
	}
	return m
}

type budgetTracker struct {
	remaining map[ContextSource]int
}

func newBudgetTracker(model SourceBudgetModel) *budgetTracker {
	return &budgetTracker{
		remaining: map[ContextSource]int{
			ContextSourcePreamble:            model.PreambleBytes,
			ContextSourceGlobalAgentsMD:      model.GlobalAgentsBytes,
			ContextSourceProjectAgentsMD:     model.ProjectAgentsBytes,
			ContextSourceProjectContext:      model.ProjectContextBytes,
			ContextSourceSkill:               model.SkillBytes,
			ContextSourceDurableContext:      model.DurableContextBytes,
			ContextSourceConversation:        model.ConversationBytes,
			ContextSourceConversationSummary: model.ConversationSummaryBytes,
			ContextSourceToolResult:          model.ToolResultBytes,
			ContextSourceToolSummary:         model.ToolSummaryBytes,
			ContextSourceDelegationResult:    model.ToolSummaryBytes,
		},
	}
}

func (b *budgetTracker) take(source ContextSource, size int) (int, bool) {
	limit, ok := b.remaining[source]
	if !ok {
		limit = 0
	}
	if size <= 0 {
		return 0, false
	}
	if limit <= 0 {
		b.remaining[source] = 0
		return 0, true
	}
	if size > limit {
		b.remaining[source] = 0
		return limit, true
	}
	b.remaining[source] = limit - size
	return size, false
}
