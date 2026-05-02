package builtin

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// ScratchpadInput holds the fields the model can write to the scratchpad.
type ScratchpadInput struct {
	Goal string `json:"goal"`
	Plan string `json:"plan,omitempty"`
	Step string `json:"step,omitempty"`
	Next string `json:"next,omitempty"`
	Open string `json:"open,omitempty"`
}

// ScratchpadSchema returns the JSON schema for the scratchpad tool.
func ScratchpadSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"goal": map[string]any{"type": "string", "description": "Current task goal"},
			"plan": map[string]any{"type": "string", "description": "High-level plan to achieve goal"},
			"step": map[string]any{"type": "string", "description": "Current step being executed"},
			"next": map[string]any{"type": "string", "description": "Next step after current"},
			"open": map[string]any{"type": "string", "description": "Open questions or blockers"},
		},
		"required":             []string{"goal"},
		"additionalProperties": false,
	}
}

// NewScratchpadTool creates a ToolDef for the scratchpad built-in.
func NewScratchpadTool(_ Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "scratchpad",
		Description:     "Record your current working state. Call at the start of each turn to track goal, plan, progress, and open questions. This state persists across context compaction.",
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
				"status": "ok",
				"goal":   in.Goal,
				"plan":   in.Plan,
				"step":   in.Step,
				"next":   in.Next,
				"open":   in.Open,
			}, nil
		},
	}
}
