package delegation

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/usagestats"
)

// DelegateToolName is the registered name of the delegate tool.
const DelegateToolName = "delegate"

const delegateTimeoutFloor = time.Minute

// agentCounter is a process-local monotonic counter for agent ID generation.
var agentCounter atomic.Uint64

// idGen is the agent ID generator; tests may override for determinism.
var idGen = func() string {
	return fmt.Sprintf("child-%d", agentCounter.Add(1))
}

func generateAgentID() string {
	return idGen()
}

// DelegateToolDef returns a ToolDef for the delegate tool with the given in-process handler.
func DelegateToolDef(handler func(ctx context.Context, input map[string]any) (any, error)) tool.ToolDef {
	return tool.ToolDef{
		Name:        DelegateToolName,
		Description: "Spawn an isolated sub-agent to complete a task. Returns structured result. The sub-agent cannot itself delegate further.",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":          map[string]any{"type": "string", "description": "Required. Self-contained task with objective, expected deliverable, constraints, and success criteria."},
				"context":       map[string]any{"type": "string", "description": "Optional. File paths, symbols, or background the sub-agent needs. Use this for lengthy context to keep the task focused."},
				"system_prompt": map[string]any{"type": "string", "description": "Optional system prompt override."},
				"max_turns":     map[string]any{"type": "integer", "description": "Optional max turns (cannot exceed default limit)."},
				"timeout":       map[string]any{"type": "string", "description": "Optional timeout duration string (e.g. '30s')."},
				"work_dir":      map[string]any{"type": "string", "description": "Optional absolute path to use as the child agent's working directory. When provided, scopes all file-path resolution for the child agent (read, mutate, glob, grep, bash) to this directory instead of the parent's working directory. Set this when delegating to a sub-agent that will operate inside a git worktree."},
			},
			"required": []any{"task"},
		},
		Handler: handler,
	}
}

// DelegateHandlerDeps holds dependencies for the delegate tool handler.
type DelegateHandlerDeps struct {
	Provider             provider.Provider
	ParentReg            *tool.Registry
	SubAgentCfg          config.SubAgentConfig
	Events               output.EventSink
	Runner               AgentRunner
	WorkDir              string
	HomeDir              string
	ProjectContextConfig config.ProjectContextConfig
	ResolvedModel        provider.ResolvedModel
	MaxTokens            *int
	StreamingPreferred   bool
	CaveHuman            bool
	TraceLogger          *TraceLogger
	SessionStore         *SessionStore
	// Sandbox is the parent sandbox. Sub-agents inherit it unchanged so that
	// child sandbox permissions cannot exceed parent permissions.
	Sandbox tool.SandboxWrapper
	// UsageRecorder is the singleton recorder shared across the process for cache-hit-rate tracking.
	UsageRecorder *usagestats.Recorder
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

		var overrides DelegationLimits
		if v, ok := input["max_turns"].(float64); ok {
			overrides.MaxTurns = int(v)
		}
		if v, ok := input["timeout"].(string); ok && v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("delegate: invalid timeout %q: %w", v, err)
			}
			if d > 0 && d < delegateTimeoutFloor {
				d = delegateTimeoutFloor
			}
			overrides.Timeout = d
		}

		workDir, err := resolveWorkDir(deps.WorkDir, input)
		if err != nil {
			return nil, err
		}

		agentID := generateAgentID()
		spec := DelegationSpec{
			Task: task, Context: contextStr, SystemPrompt: systemPrompt,
			AgentID: agentID, Limits: overrides,
		}

		req, limits, err := BuildChildRun(ctx, BootstrapDeps{
			Provider:             deps.Provider,
			ParentReg:            deps.ParentReg,
			SubAgentCfg:          deps.SubAgentCfg,
			Events:               deps.Events,
			WorkDir:              workDir,
			HomeDir:              deps.HomeDir,
			ProjectContextConfig: deps.ProjectContextConfig,
			ResolvedModel:        deps.ResolvedModel,
			MaxTokens:            deps.MaxTokens,
			StreamingPreferred:   deps.StreamingPreferred,
			CaveHuman:            deps.CaveHuman,
			Sandbox:              deps.Sandbox,
			UsageRecorder:        deps.UsageRecorder,
		}, spec)
		if err != nil {
			return nil, fmt.Errorf("delegate: build child run: %w", err)
		}
		spec.Limits = limits

		result, state, err := SpawnDelegate(ctx, spec, req, deps.Runner, deps.Events, deps.TraceLogger)
		if err == nil && deps.SessionStore != nil {
			saveChildSession(deps.SessionStore, spec, req, state)
		}
		if err != nil {
			if result != (tool.ExecutionResult{}) {
				return result, nil
			}
			return nil, fmt.Errorf("delegate failed: %w", err)
		}
		return result, nil
	}
}

func resolveWorkDir(defaultDir string, input map[string]any) (string, error) {
	v, ok := input["work_dir"].(string)
	if !ok || v == "" {
		return defaultDir, nil
	}
	if !filepath.IsAbs(v) {
		return "", fmt.Errorf("delegate: work_dir must be an absolute path, got %q", v)
	}
	return v, nil
}

func saveChildSession(store *SessionStore, spec DelegationSpec, req agent.RunRequest, state agent.RunState) {
	store.Save(&ChildSession{
		Spec:          spec,
		Request:       req,
		Conversation:  state.Conversation,
		TurnCount:     state.TurnCount,
		TokenCount:    state.TokenCount,
		ToolCallCount: countToolCalls(state.Conversation),
	})
}
