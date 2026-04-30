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

func (a Assembler) planSourceAssembly() sourcePlan {
	return sourcePlan{
		Steps: []sourcePlanStep{
			blockSourceStep(plannedSourcePreamble, plannedSourcePlacementCore, func(_ context.Context, state *assemblyState) error {
				a.appendPreamble(state)
				return nil
			}),
			blockSourceStep(plannedSourceAgents, plannedSourcePlacementCore, func(_ context.Context, state *assemblyState) error {
				return a.appendAgents(state)
			}),
			blockSourceStep(plannedSourceProjectContext, plannedSourcePlacementCore, func(_ context.Context, state *assemblyState) error {
				return a.appendProjectContext(state)
			}),
			blockSourceStep(plannedSourceSkills, plannedSourcePlacementCore, func(ctx context.Context, state *assemblyState) error {
				return a.appendSkills(ctx, state)
			}),
			blockSourceStep(plannedSourceDurableContext, plannedSourcePlacementCore, func(_ context.Context, state *assemblyState) error {
				a.appendDurableContext(state)
				return nil
			}),
			passThroughSourceStep(plannedSourceConversation, plannedSourcePlacementConversation, func(_ context.Context, state *assemblyState) error {
				a.appendConversation(state)
				return nil
			}),
			blockSourceStep(plannedSourceToolSummaries, plannedSourcePlacementToolSummaries, func(_ context.Context, state *assemblyState) error {
				a.appendToolSummaries(state)
				return nil
			}),
		},
	}
}

func blockSourceStep(kind plannedSourceKind, placement plannedSourcePlacement, apply func(context.Context, *assemblyState) error) sourcePlanStep {
	return sourcePlanStep{
		Kind:        kind,
		Placement:   placement,
		PassThrough: false,
		Apply:       apply,
	}
}

func passThroughSourceStep(kind plannedSourceKind, placement plannedSourcePlacement, apply func(context.Context, *assemblyState) error) sourcePlanStep {
	return sourcePlanStep{
		Kind:        kind,
		Placement:   placement,
		PassThrough: true,
		Apply:       apply,
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
