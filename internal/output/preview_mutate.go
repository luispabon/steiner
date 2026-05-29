package output

import (
	"encoding/json"
	"fmt"
	"strings"
)

type mutatePreviewPayload struct {
	Created           []string           `json:"created"`
	Modified          []string           `json:"modified"`
	Deleted           []string           `json:"deleted"`
	Moved             []patchPreviewMove `json:"moved"`
	OperationsApplied int                `json:"operations_applied"`
	OperationsFailed  int                `json:"operations_failed"`
	DryRun            bool               `json:"dry_run"`
}

// buildMutatePreviewImpl constructs a ToolPreview for the mutate tool by
// combining raw input arguments (to know operation details) with the result
// JSON (to know applied/failed counts and affected files).
func buildMutatePreviewImpl(arguments map[string]any, result string) ToolPreview {
	preview := ToolPreview{Kind: ToolPreviewKindMutate}

	if ops, ok := arguments["operations"].([]any); ok {
		preview.MutateOperations = make([]ToolPreviewMutateOperation, 0, len(ops))
		for _, rawOp := range ops {
			opMap, ok := rawOp.(map[string]any)
			if !ok {
				continue
			}
			op := ToolPreviewMutateOperation{
				Type: getMapString(opMap, "type"),
				Path: getMapString(opMap, "path"),
				From: getMapString(opMap, "from"),
				To:   getMapString(opMap, "to"),
				Line: getMapInt(opMap, "line"),
			}
			if content, ok := opMap["content"].(string); ok {
				op.Content = content
			}
			if oldString, ok := opMap["old_string"].(string); ok {
				op.OldString = oldString
			}
			if newString, ok := opMap["new_string"].(string); ok {
				op.NewString = newString
			}
			preview.MutateOperations = append(preview.MutateOperations, op)
		}
	}

	if strings.TrimSpace(result) == "" {
		return preview
	}

	var payload mutatePreviewPayload
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return preview
	}

	preview.PatchAdded = payload.Created
	preview.PatchModified = payload.Modified
	preview.PatchDeleted = payload.Deleted
	preview.PatchMoved = buildPatchMoves(payload.Moved)
	preview.HunksApplied = payload.OperationsApplied
	preview.HunksFailed = payload.OperationsFailed
	return preview
}

func getMapString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	if v, ok := m[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func getMapInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		var i int
		_, _ = fmt.Sscanf(v, "%d", &i)
		return i
	default:
		return 0
	}
}
