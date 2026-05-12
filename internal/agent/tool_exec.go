package agent

import (
	"context"
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
	case "write", "write_file", "edit", "apply_patch":
	default:
		return
	}
	if m, ok := result.(mutated); ok && !m.WasMutated() {
		return
	}
	path, ok := input["path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return
	}
	recorder, ok := cm.(MutationRecorder)
	if !ok {
		return
	}
	recorder.RecordMutation(path)
}

func contextCancellationState(ctx context.Context, state RunState) (RunState, bool) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		state.StopReason = StopReasonCancelled
		return state, true
	}
	return RunState{}, false
}
