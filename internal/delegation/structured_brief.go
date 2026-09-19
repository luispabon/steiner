package delegation

import (
	"fmt"
	"strings"
)

// structuredBrief holds the decoded fields from a structured task dispatch.
type structuredBrief struct {
	Objective       string   `json:"objective"`
	Ctx             string   `json:"context"`
	Deliverable     string   `json:"deliverable"`
	Constraints     []string `json:"constraints"`
	SuccessCriteria []string `json:"success_criteria"`
	Checks          []string `json:"checks"`
}

func parseStructuredBrief(prefix string, input map[string]any) (structuredBrief, error) {
	brief := structuredBrief{
		Constraints:     []string{},
		SuccessCriteria: []string{},
		Checks:          []string{},
	}

	objective, _ := input["objective"].(string)
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return structuredBrief{}, fmt.Errorf("%s: objective is required and must be non-empty", prefix)
	}
	brief.Objective = objective

	contextStr, _ := input["context"].(string)
	contextStr = strings.TrimSpace(contextStr)
	if contextStr == "" {
		return structuredBrief{}, fmt.Errorf("%s: context is required and must be non-empty", prefix)
	}
	brief.Ctx = contextStr

	deliverable, _ := input["deliverable"].(string)
	deliverable = strings.TrimSpace(deliverable)
	if deliverable == "" {
		return structuredBrief{}, fmt.Errorf("%s: deliverable is required and must be non-empty", prefix)
	}
	brief.Deliverable = deliverable

	var err error
	if brief.Constraints, err = parseStringList(prefix, "constraints", input); err != nil {
		return structuredBrief{}, err
	}
	if brief.SuccessCriteria, err = parseStringList(prefix, "success_criteria", input); err != nil {
		return structuredBrief{}, err
	}
	if brief.Checks, err = parseStringList(prefix, "checks", input); err != nil {
		return structuredBrief{}, err
	}

	return brief, nil
}

func parseStringList(prefix, field string, input map[string]any) ([]string, error) {
	items := []string{}
	raw, ok := input[field].([]any)
	if !ok {
		return items, nil
	}
	for i, item := range raw {
		value, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s: %s[%d] is not a string", prefix, field, i)
		}
		items = append(items, value)
	}
	return items, nil
}

// assembleTaskContent renders a structuredBrief into a deterministic markdown
// message for the child agent. The output always uses the fixed field order
// (Objective, Context, Deliverable, Constraints, Success criteria, Checks)
// and omits empty optional sections entirely. This text becomes part of the
// cached prompt prefix, so determinism is essential.
func writeListItemSection(buf *strings.Builder, title string, items []string) {
	buf.WriteString("\n\n## ")
	buf.WriteString(title)
	buf.WriteString("\n\n")
	for i, item := range items {
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString("- ")
		buf.WriteString(item)
	}
}

func assembleTaskContent(b structuredBrief) string {
	var buf strings.Builder

	buf.WriteString("## Objective\n\n")
	buf.WriteString(b.Objective)
	buf.WriteString("\n\n")

	buf.WriteString("## Context\n\n")
	buf.WriteString(b.Ctx)
	buf.WriteString("\n\n")

	buf.WriteString("## Deliverable\n\n")
	buf.WriteString(b.Deliverable)

	if len(b.Constraints) > 0 {
		writeListItemSection(&buf, "Constraints", b.Constraints)
	}

	if len(b.SuccessCriteria) > 0 {
		writeListItemSection(&buf, "Success criteria", b.SuccessCriteria)
	}

	if len(b.Checks) > 0 {
		writeListItemSection(&buf, "Checks", b.Checks)
	}

	return buf.String()
}
