package prompt

import "fmt"

const (
	defaultPreambleBudgetBytes      = 4096
	defaultGlobalAgentsBudgetBytes  = 2048
	defaultProjectAgentsBudgetBytes = 8192
	defaultSkillBudgetBytes         = 2048
	defaultToolResultBudgetBytes    = 2048
	defaultToolSummaryBudgetBytes   = 1024
	defaultCompactionSummaryBytes   = 1024
)

// DefaultAssemblyPolicy returns the default prompt assembly policy.
func DefaultAssemblyPolicy() AssemblyPolicy {
	return AssemblyPolicy{
		Budgets: SourceBudgetModel{
			PreambleBytes:       defaultPreambleBudgetBytes,
			GlobalAgentsBytes:   defaultGlobalAgentsBudgetBytes,
			ProjectAgentsBytes:  defaultProjectAgentsBudgetBytes,
			ProjectContextBytes: defaultProjectContextBudgetBytes,
			SkillBytes:          defaultSkillBudgetBytes,
			ToolResultBytes:     defaultToolResultBudgetBytes,
			ToolSummaryBytes:    defaultToolSummaryBudgetBytes,
		},
		Compaction:  CompactionPolicy{SummaryBytes: defaultCompactionSummaryBytes},
		ToolSummary: ToolSummaryPolicy{MaxBytes: defaultToolSummaryBudgetBytes},
	}
}

func normalizeAssemblyPolicy(policy AssemblyPolicy) (AssemblyPolicy, error) {
	defaults := DefaultAssemblyPolicy()
	if err := validateAssemblyPolicy(policy); err != nil {
		return AssemblyPolicy{}, err
	}
	policy.Budgets = normalizeSourceBudgets(policy.Budgets, defaults.Budgets)
	policy.Compaction = normalizeCompactionPolicy(policy.Compaction, defaults.Compaction)
	policy.ToolSummary = normalizeToolSummaryPolicy(policy.ToolSummary, defaults.ToolSummary)
	return policy, nil
}

func validateAssemblyPolicy(policy AssemblyPolicy) error {
	if policy.Budgets.PreambleBytes < 0 ||
		policy.Budgets.GlobalAgentsBytes < 0 ||
		policy.Budgets.ProjectAgentsBytes < 0 ||
		policy.Budgets.ProjectContextBytes < 0 ||
		policy.Budgets.SkillBytes < 0 ||
		policy.Budgets.ToolResultBytes < 0 ||
		policy.Budgets.ToolSummaryBytes < 0 {
		return fmt.Errorf("assembly budgets must not be negative")
	}
	if policy.Compaction.SummaryBytes < 0 {
		return fmt.Errorf("summary bytes must not be negative")
	}
	if policy.ToolSummary.MaxBytes < 0 {
		return fmt.Errorf("tool summary max bytes must not be negative")
	}
	return nil
}

func normalizeSourceBudgets(budgets, defaults SourceBudgetModel) SourceBudgetModel {
	if budgets.PreambleBytes == 0 {
		budgets.PreambleBytes = defaults.PreambleBytes
	}
	if budgets.GlobalAgentsBytes == 0 {
		budgets.GlobalAgentsBytes = defaults.GlobalAgentsBytes
	}
	if budgets.ProjectAgentsBytes == 0 {
		budgets.ProjectAgentsBytes = defaults.ProjectAgentsBytes
	}
	if budgets.ProjectContextBytes == 0 {
		budgets.ProjectContextBytes = defaults.ProjectContextBytes
	}
	if budgets.SkillBytes == 0 {
		budgets.SkillBytes = defaults.SkillBytes
	}
	if budgets.ToolResultBytes == 0 {
		budgets.ToolResultBytes = defaults.ToolResultBytes
	}
	if budgets.ToolSummaryBytes == 0 {
		budgets.ToolSummaryBytes = defaults.ToolSummaryBytes
	}
	return budgets
}

func normalizeCompactionPolicy(compaction, defaults CompactionPolicy) CompactionPolicy {
	if compaction.SummaryBytes == 0 {
		compaction.SummaryBytes = defaults.SummaryBytes
	}
	return compaction
}

func normalizeToolSummaryPolicy(toolSummary, defaults ToolSummaryPolicy) ToolSummaryPolicy {
	if toolSummary.MaxBytes == 0 {
		toolSummary.MaxBytes = defaults.MaxBytes
	}
	return toolSummary
}

func validateAssemblyOptions(opts AssemblyOptions) error {
	if opts.ProjectContextBudgetBytes < 0 {
		return fmt.Errorf("project context budget must not be negative")
	}
	return nil
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
			ContextSourcePreamble:         model.PreambleBytes,
			ContextSourceGlobalAgentsMD:   model.GlobalAgentsBytes,
			ContextSourceProjectAgentsMD:  model.ProjectAgentsBytes,
			ContextSourceProjectContext:   model.ProjectContextBytes,
			ContextSourceSkill:            model.SkillBytes,
			ContextSourceToolResult:       model.ToolResultBytes,
			ContextSourceToolSummary:      model.ToolSummaryBytes,
			ContextSourceDelegationResult: model.ToolSummaryBytes,
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
