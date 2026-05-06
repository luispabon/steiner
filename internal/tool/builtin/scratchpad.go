package builtin

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// ScratchpadInput holds the four fields the model can write to the scratchpad.
type ScratchpadInput struct {
	Intent    string `json:"intent"`
	Decisions string `json:"decisions"`
	Open      string `json:"open"`
	Next      string `json:"next"`
}

// ScratchpadSchema returns the JSON schema for the scratchpad tool.
func ScratchpadSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"intent":    map[string]any{"type": "string", "description": "What is being done and why"},
			"decisions": map[string]any{"type": "string", "description": "Key decisions made so far (append only; steiner merges history)"},
			"open":      map[string]any{"type": "string", "description": "Unresolved questions or risks"},
			"next":      map[string]any{"type": "string", "description": "Planned next action"},
		},
		"required":             []string{"intent", "decisions", "open", "next"},
		"additionalProperties": false,
	}
}

// NewScratchpadTool creates a ToolDef for the scratchpad built-in.
func NewScratchpadTool(_ Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "scratchpad",
		Description:     "Record your current working state. Call on every turn without exception to track intent, decisions, open questions, and next action. This state persists across context compaction.",
		ParameterSchema: ScratchpadSchema(),
		Approval:        config.ApprovalModeAuto,
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			in, err := decodeScratchpadInput(input)
			if err != nil {
				return nil, fmt.Errorf("scratchpad: %w", err)
			}
			return map[string]string{
				"status":    "ok",
				"intent":    in.Intent,
				"decisions": in.Decisions,
				"open":      in.Open,
				"next":      in.Next,
			}, nil
		},
	}
}

func decodeScratchpadInput(raw map[string]any) (ScratchpadInput, error) {
	input := ScratchpadInput{}
	if raw == nil {
		return input, fmt.Errorf("input is required")
	}

	var ok bool
	if input.Intent, ok = requiredStringField(raw, "intent"); !ok {
		return input, fmt.Errorf("intent is required")
	}
	if input.Decisions, ok = requiredStringField(raw, "decisions"); !ok {
		return input, fmt.Errorf("decisions is required")
	}
	if input.Open, ok = requiredStringField(raw, "open"); !ok {
		return input, fmt.Errorf("open is required")
	}
	if input.Next, ok = requiredStringField(raw, "next"); !ok {
		return input, fmt.Errorf("next is required")
	}
	if input.Intent == "" {
		return input, fmt.Errorf("intent is required")
	}
	return input, nil
}

func requiredStringField(raw map[string]any, key string) (string, bool) {
	value, ok := raw[key]
	if !ok || value == nil {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	return text, true
}
