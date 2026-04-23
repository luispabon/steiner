package delegation

import (
	"context"
	"fmt"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

const DelegateToolName = "delegate"

// DelegateToolDef returns a ToolDef for the delegate tool with the given in-process handler.
func DelegateToolDef(handler func(ctx context.Context, input map[string]any) (any, error)) tool.ToolDef {
	return tool.ToolDef{
		Name:        DelegateToolName,
		Description: "Spawn an isolated sub-agent to complete a task. Returns structured result. The sub-agent cannot itself delegate further.",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":          map[string]any{"type": "string", "description": "Required. The task for the sub-agent."},
				"context":       map[string]any{"type": "string", "description": "Optional additional context."},
				"system_prompt": map[string]any{"type": "string", "description": "Optional system prompt override."},
				"model":         map[string]any{"type": "string", "description": "Optional model override."},
				"max_turns":     map[string]any{"type": "integer", "description": "Optional max turns (cannot exceed default limit)."},
				"timeout":       map[string]any{"type": "string", "description": "Optional timeout duration string (e.g. '30s')."},
			},
			"required": []any{"task"},
		},
		Handler:  handler,
		Approval: config.ApprovalModeAuto,
	}
}

// DelegateHandlerDeps holds dependencies for the delegate tool handler.
type DelegateHandlerDeps struct {
	Provider    provider.Provider
	ParentReg   *tool.Registry
	SubAgentCfg config.SubAgentConfig
	Events      output.EventSink
	Runner      AgentRunner
}

// NewDelegateHandler returns the in-process handler for the delegate tool.
func NewDelegateHandler(deps DelegateHandlerDeps) func(ctx context.Context, input map[string]any) (any, error) {
	return func(ctx context.Context, input map[string]any) (any, error) {
		task, _ := input["task"].(string)
		if task == "" {
			return nil, fmt.Errorf("delegate: task is required")
		}

		contextStr, _ := input["context"].(string)
		systemPrompt, _ := input["system_prompt"].(string)
		model, _ := input["model"].(string)

		baseLimits := DefaultLimits(deps.SubAgentCfg)
		var overrides DelegationLimits
		if v, ok := input["max_turns"].(float64); ok {
			overrides.MaxTurns = int(v)
		}
		if v, ok := input["timeout"].(string); ok && v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				overrides.Timeout = d
			}
		}
		limits := ApplyOverrides(baseLimits, overrides)

		agentID := fmt.Sprintf("child-%d", time.Now().UnixNano())
		spec := DelegationSpec{
			Task: task, Context: contextStr, SystemPrompt: systemPrompt,
			Model: model, Limits: limits, AgentID: agentID,
		}

		childReg := BuildChildToolRegistry(deps.ParentReg, DelegateToolName)
		agentLimits := agent.Limits{MaxTurns: limits.MaxTurns, MaxTokens: limits.OutputLimitTokens}
		req := BuildChildRunRequest(spec, deps.Provider, childReg, agentLimits, deps.Events)

		result, err := SpawnDelegate(ctx, spec, req, deps.Runner, deps.Events)
		if err != nil {
			return nil, fmt.Errorf("delegate failed: %w", err)
		}
		return result, nil
	}
}
