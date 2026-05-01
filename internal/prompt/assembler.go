package prompt

import (
	"context"
	"fmt"
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
	plan := a.planSourceAssembly()
	return plan.render(ctx, a.policy, a.opts)
}

func (a Assembler) String() string {
	return fmt.Sprintf("Assembler{conversation=%d tool_results=%d}", len(a.opts.Conversation), len(a.opts.ToolResults))
}
