package agent

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/luispabon/steiner/internal/tool"
)

func formatToolError(err error) string {
	var tee *tool.ToolExecutionError
	if ok := errors.As(err, &tee); ok {
		details := map[string]any{
			"exit_code": tee.ExitCode,
			"stdout":    tee.Output.Stdout.Preview,
			"stderr":    tee.Output.Stderr.Preview,
		}
		if tee.Output.Stdout.Summary() != "" || tee.Output.Stderr.Summary() != "" {
			details["stdout"] = tee.Output.Stdout.Summary()
			details["stderr"] = tee.Output.Stderr.Summary()
		}
		return marshalToolErrorEnvelope(tee.Kind, tee.Message, details)
	}
	return marshalToolErrorEnvelope("tool_error", err.Error(), nil)
}

func marshalToolErrorEnvelope(kind, message string, details map[string]any) string {
	envelope := tool.JSONEnvelope{
		OK: false,
		Error: &tool.JSONEnvelopeError{
			Kind:    kind,
			Message: message,
			Details: details,
		},
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Sprintf(`{"ok":false,"error":{"kind":"%s","message":"%s"}}`, kind, message)
	}
	return string(data)
}
