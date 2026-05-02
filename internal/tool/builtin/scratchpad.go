package builtin

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// ScratchpadInput holds the fields the model can write to the scratchpad.
type ScratchpadInput struct {
	Goal      string `json:"goal"`
	Plan      string `json:"plan"`
	Step      string `json:"step"`
	Decisions string `json:"decisions"`
	Files     string `json:"files"`
	Open      string `json:"open"`
	Next      string `json:"next"`
}

// ScratchpadSchema returns the JSON schema for the scratchpad tool.
func ScratchpadSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"goal":      map[string]any{"type": "string", "description": "Current task goal (one line, stable)"},
			"plan":      map[string]any{"type": "string", "description": "High-level plan to achieve goal"},
			"step":      map[string]any{"type": "string", "description": "The specific action just completed or about to take"},
			"decisions": map[string]any{"type": "string", "description": "Key choices made this turn and why (append only; steiner merges history)"},
			"files":     map[string]any{"type": "string", "description": "Files read or modified with status (read / modified / stale)"},
			"open":      map[string]any{"type": "string", "description": "Unresolved problems or unknowns blocking progress"},
			"next":      map[string]any{"type": "string", "description": "The single next action to take after this turn"},
		},
		"required":             []string{"goal", "plan", "step", "decisions", "files", "open", "next"},
		"additionalProperties": false,
	}
}

// NewScratchpadTool creates a ToolDef for the scratchpad built-in.
func NewScratchpadTool(_ Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "scratchpad",
		Description:     "Record your current working state. Call on every turn without exception to track goal, plan, progress, decisions, and open questions. This state persists across context compaction.",
		ParameterSchema: ScratchpadSchema(),
		Approval:        config.ApprovalModeAuto,
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[ScratchpadInput](input)
			if err != nil {
				return nil, fmt.Errorf("scratchpad: %w", err)
			}
			if in.Goal == "" {
				return nil, fmt.Errorf("scratchpad: goal is required")
			}
			return map[string]string{
				"status":    "ok",
				"goal":      in.Goal,
				"plan":      in.Plan,
				"step":      in.Step,
				"decisions": in.Decisions,
				"files":     in.Files,
				"open":      in.Open,
				"next":      in.Next,
			}, nil
		},
	}
}
