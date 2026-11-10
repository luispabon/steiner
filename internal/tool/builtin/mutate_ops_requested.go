package builtin

import "strings"

// invalidOpLabel is the fixed ops_requested label for an operation whose type
// is missing, not a string, or not one of validOpTypes.
const invalidOpLabel = "invalid"

// mutateOpsRequested counts the operation types a mutate call asked for, keyed
// by the fixed labels in validOpTypes plus invalidOpLabel. It reads only type
// fields: paths, content, and other field values never reach the tool
// diagnostics stream. A nil result means operations is absent or not an array,
// so the record omits ops_requested rather than guessing.
func mutateOpsRequested(input map[string]any) map[string]int {
	ops, ok := input["operations"].([]any)
	if !ok {
		return nil
	}
	counts := make(map[string]int, len(validOpTypes)+1)
	for _, raw := range ops {
		counts[requestedOpLabel(raw)]++
	}
	return counts
}

// requestedOpLabel maps one raw operation to a fixed label. Trimming matches
// the planner's op dispatch, and only validOpTypes pass through unchanged.
func requestedOpLabel(raw any) string {
	op, ok := raw.(map[string]any)
	if !ok {
		return invalidOpLabel
	}
	opType, ok := op["type"].(string)
	if !ok {
		return invalidOpLabel
	}
	opType = strings.TrimSpace(opType)
	if _, ok := allowedFields[opType]; !ok {
		return invalidOpLabel
	}
	return opType
}
