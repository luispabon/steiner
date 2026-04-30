package prompt

import "context"

type plannedSourceKind string

const (
	plannedSourcePreamble       plannedSourceKind = "preamble"
	plannedSourceAgents         plannedSourceKind = "agents"
	plannedSourceProjectContext plannedSourceKind = "project_context"
	plannedSourceSkills         plannedSourceKind = "skills"
	plannedSourceDurableContext plannedSourceKind = "durable_context"
	plannedSourceConversation   plannedSourceKind = "conversation"
	plannedSourceToolSummaries  plannedSourceKind = "tool_summaries"
)

type sourcePlan struct {
	Steps []sourcePlanStep
}

type sourcePlanStep struct {
	Kind  plannedSourceKind
	Apply func(context.Context, *assemblyState) error
}

func (a Assembler) planSourceAssembly() sourcePlan {
	return sourcePlan{
		Steps: []sourcePlanStep{
			{
				Kind: plannedSourcePreamble,
				Apply: func(_ context.Context, state *assemblyState) error {
					a.appendPreamble(state)
					return nil
				},
			},
			{
				Kind: plannedSourceAgents,
				Apply: func(_ context.Context, state *assemblyState) error {
					return a.appendAgents(state)
				},
			},
			{
				Kind: plannedSourceProjectContext,
				Apply: func(_ context.Context, state *assemblyState) error {
					return a.appendProjectContext(state)
				},
			},
			{
				Kind: plannedSourceSkills,
				Apply: func(ctx context.Context, state *assemblyState) error {
					return a.appendSkills(ctx, state)
				},
			},
			{
				Kind: plannedSourceDurableContext,
				Apply: func(_ context.Context, state *assemblyState) error {
					a.appendDurableContext(state)
					return nil
				},
			},
			{
				Kind: plannedSourceConversation,
				Apply: func(_ context.Context, state *assemblyState) error {
					a.appendConversation(state)
					return nil
				},
			},
			{
				Kind: plannedSourceToolSummaries,
				Apply: func(_ context.Context, state *assemblyState) error {
					a.appendToolSummaries(state)
					return nil
				},
			},
		},
	}
}

func (a Assembler) renderSourcePlan(ctx context.Context, plan sourcePlan) (Assembly, error) {
	state := newAssemblyState(a.policy, a.opts)

	for _, step := range plan.Steps {
		if err := step.Apply(ctx, &state); err != nil {
			return Assembly{}, err
		}
		state.renderBlocks()
	}

	return Assembly{
		Messages: state.messages,
		Blocks:   state.blocks,
	}, nil
}
