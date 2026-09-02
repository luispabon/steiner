package main

func subAgentTask(agentType, objective, context, deliverable string) map[string]any {
	return map[string]any{
		"type": agentType, "objective": objective, "context": context, "deliverable": deliverable,
		"constraints": []any{}, "success_criteria": []any{}, "checks": []any{},
	}
}
