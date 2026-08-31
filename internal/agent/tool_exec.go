package agent

import (
	"context"
	"encoding/json"
	"strings"
)

type mutated interface {
	WasMutated() bool
}

type mutatedPaths interface {
	MutatedPaths() []string
}

func recordMutationForContextManager(cm *ContextStateManager, toolName string, _ map[string]any, result any) {
	if cm == nil || !strings.EqualFold(strings.TrimSpace(toolName), "mutate") {
		return
	}
	if m, ok := result.(mutated); ok && !m.WasMutated() {
		return
	}
	for _, path := range mutationResultPaths(result) {
		cm.RecordMutation(path)
	}
}

func mutationResultPaths(result any) []string {
	if mp, ok := result.(mutatedPaths); ok {
		return mp.MutatedPaths()
	}
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
