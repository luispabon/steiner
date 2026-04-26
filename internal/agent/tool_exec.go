package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	if !filepath.IsAbs(path) {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil
		}
		path = absPath
	}
	path = filepath.Clean(path)
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

func contextCancellationState(ctx context.Context, state RunState) (RunState, bool) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		state.StopReason = StopReasonCancelled
		return state, true
	}
	return RunState{}, false
}
