package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// ScratchpadInput holds the fields the model can write to the scratchpad.
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

	input.Intent = stringField(raw, "intent")
	input.Decisions = stringField(raw, "decisions")
	input.Open = stringField(raw, "open")
	input.Next = stringField(raw, "next")

	if strings.TrimSpace(input.Intent) == "" {
		input.Intent = strings.TrimSpace(strings.Join(nonEmptyStrings([]string{
			stringField(raw, "goal"),
			stringField(raw, "plan"),
			stringField(raw, "step"),
		}), " "))
	}

	if strings.TrimSpace(input.Intent) == "" {
		return input, fmt.Errorf("intent is required")
	}
	if strings.TrimSpace(input.Decisions) == "" {
		input.Decisions = stringField(raw, "decisions")
	}
	if strings.TrimSpace(input.Open) == "" {
		input.Open = stringField(raw, "open")
	}
	if strings.TrimSpace(input.Next) == "" {
		input.Next = stringField(raw, "next")
	}
	return input, nil
}

func stringField(raw map[string]any, key string) string {
	value, ok := raw[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func nonEmptyStrings(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
