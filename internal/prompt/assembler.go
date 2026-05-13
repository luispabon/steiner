package prompt

import (
	"context"
	"fmt"
)

type assembler struct {
	opts   AssemblyOptions
	policy AssemblyPolicy
}

func newAssembler(opts AssemblyOptions) (assembler, error) {
	policy, err := normalizeAssemblyPolicy(opts.Policy)
	if err != nil {
		return assembler{}, err
	}
	policy.Budgets = policy.Budgets.withProjectContextBudget(opts.ProjectContextBudgetBytes)
	return assembler{opts: opts, policy: policy}, nil
}

func (a assembler) Assemble(ctx context.Context) (Assembly, error) {
	plan := a.planSourceAssembly()
	return plan.render(ctx, a.policy, a.opts)
}

func (a assembler) String() string {
	return fmt.Sprintf("assembler{conversation=%d tool_results=%d}", len(a.opts.Conversation), len(a.opts.ToolResults))
}
