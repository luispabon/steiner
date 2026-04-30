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
	Sources []plannedSourceKind
}

func (a Assembler) planSourceAssembly() sourcePlan {
	return sourcePlan{
		Sources: []plannedSourceKind{
			plannedSourcePreamble,
			plannedSourceAgents,
			plannedSourceProjectContext,
			plannedSourceSkills,
			plannedSourceDurableContext,
			plannedSourceConversation,
			plannedSourceToolSummaries,
		},
	}
}

func (a Assembler) renderSourcePlan(ctx context.Context, plan sourcePlan) (Assembly, error) {
	state := newAssemblyState(a.policy, a.opts)

	for _, source := range plan.Sources {
		switch source {
		case plannedSourcePreamble:
			a.appendPreamble(&state)
		case plannedSourceAgents:
			if err := a.appendAgents(&state); err != nil {
				return Assembly{}, err
			}
		case plannedSourceProjectContext:
			if err := a.appendProjectContext(&state); err != nil {
				return Assembly{}, err
			}
		case plannedSourceSkills:
			if err := a.appendSkills(ctx, &state); err != nil {
				return Assembly{}, err
			}
		case plannedSourceDurableContext:
			a.appendDurableContext(&state)
		case plannedSourceConversation:
			a.appendConversation(&state)
		case plannedSourceToolSummaries:
			a.appendToolSummaries(&state)
		}
	}

	return Assembly{
		Messages: state.messages,
		Blocks:   state.blocks,
	}, nil
}
