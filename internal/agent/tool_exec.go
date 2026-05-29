package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
)

func writeTargetExistedBefore(toolName string, input map[string]any) *bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "write", "write_file":
	default:
		return nil
	}
	path, ok := input["path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return nil
	}
	path, ok = normalizeTrackedPath(path)
	if !ok {
		return nil
	}
	_, err := os.Stat(path)
	if err == nil {
		existed := true
		return &existed
	}
	if errors.Is(err, os.ErrNotExist) {
		existed := false
		return &existed
	}
	return nil
}

type mutated interface {
	WasMutated() bool
}

func recordMutationForContextManager(cm ContextManager, toolName string, input map[string]any, result any) {
	if cm == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "write", "write_file", "edit", "apply_patch", "mutate":
	default:
		return
	}
	if m, ok := result.(mutated); ok && !m.WasMutated() {
		return
	}
	recorder, ok := cm.(MutationRecorder)
	if !ok {
		return
	}
	if strings.EqualFold(strings.TrimSpace(toolName), "mutate") {
		for _, path := range mutationResultPaths(result) {
			recorder.RecordMutation(path)
		}
		return
	}
	path, ok := input["path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return
	}
	recorder.RecordMutation(path)
}

func mutationResultPaths(result any) []string {
	data, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	var payload struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	return payload.Paths
}

func contextCancellationState(ctx context.Context, state RunState) (RunState, bool) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		state.StopReason = StopReasonCancelled
		return state, true
	}
	return RunState{}, false
}
