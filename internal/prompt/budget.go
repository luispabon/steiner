package prompt

import "fmt"

const (
	defaultSkillBudgetBytes = 98304
)

// DefaultAssemblyPolicy returns the default prompt assembly policy.
func DefaultAssemblyPolicy() AssemblyPolicy {
	return AssemblyPolicy{
		Budgets: SourceBudgetModel{
			ProjectContextBytes: fallbackProjectContextBudgetBytes,
			SkillBytes:          defaultSkillBudgetBytes,
		},
	}
}

func normalizeAssemblyPolicy(policy AssemblyPolicy) (AssemblyPolicy, error) {
	defaults := DefaultAssemblyPolicy()
	if err := validateAssemblyPolicy(policy); err != nil {
		return AssemblyPolicy{}, err
	}
	policy.Budgets = normalizeSourceBudgets(policy.Budgets, defaults.Budgets)
	return policy, nil
}

func validateAssemblyPolicy(policy AssemblyPolicy) error {
	if policy.Budgets.ProjectContextBytes < 0 ||
		policy.Budgets.SkillBytes < 0 {
		return fmt.Errorf("assembly budgets must not be negative")
	}
	return nil
}

func normalizeSourceBudgets(budgets, defaults SourceBudgetModel) SourceBudgetModel {
	if budgets.ProjectContextBytes == 0 {
		budgets.ProjectContextBytes = defaults.ProjectContextBytes
	}
	if budgets.SkillBytes == 0 {
		budgets.SkillBytes = defaults.SkillBytes
	}
	return budgets
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
			ContextSourceProjectContext: model.ProjectContextBytes,
			ContextSourceSkill:          model.SkillBytes,
		},
	}
}

func (b *budgetTracker) take(source ContextSource, size int) int {
	limit, ok := b.remaining[source]
	if !ok {
		limit = 0
	}
	if size <= 0 {
		return 0
	}
	if limit <= 0 {
		b.remaining[source] = 0
		return 0
	}
	if size > limit {
		b.remaining[source] = 0
		return limit
	}
	b.remaining[source] = limit - size
	return size
}
