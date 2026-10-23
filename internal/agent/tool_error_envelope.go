package agent

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/luispabon/steiner/internal/tool"
)

// toolErrorKindMessage returns the kind and message reported for a failed tool
// call. tee is the executor error when err carries one, and nil otherwise.
func toolErrorKindMessage(err error) (kind, message string, tee *tool.ToolExecutionError) {
	if errors.As(err, &tee) {
		return tee.Kind, tee.Message, tee
	}
	return "tool_error", err.Error(), nil
}

func formatToolError(err error) string {
	kind, message, tee := toolErrorKindMessage(err)
	if tee != nil {
		details := map[string]any{
			"exit_code": tee.ExitCode,
			"stdout":    tee.Output.Stdout.Preview,
			"stderr":    tee.Output.Stderr.Preview,
		}
		if tee.Output.Stdout.Summary() != "" || tee.Output.Stderr.Summary() != "" {
			details["stdout"] = tee.Output.Stdout.Summary()
			details["stderr"] = tee.Output.Stderr.Summary()
		}
		return marshalToolErrorEnvelope(kind, message, details)
	}
	return marshalToolErrorEnvelope(kind, message, nil)
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
