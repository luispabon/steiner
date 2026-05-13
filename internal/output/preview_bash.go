package output

import "encoding/json"

func buildBashPreview(arguments map[string]any, result string) ToolPreview {
	var payload struct {
		ExitCode  int    `json:"exit_code"`
		Truncated bool   `json:"truncated,omitempty"`
		Output    string `json:"output"`
		Message   string `json:"message,omitempty"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return plainToolPreview()
	}

	return ToolPreview{
		Kind:      ToolPreviewKindBash,
		Command:   rawStringArg(arguments, "command"),
		ExitCode:  payload.ExitCode,
		Truncated: payload.Truncated,
		Output:    payload.Output,
		Message:   payload.Message,
	}
}
