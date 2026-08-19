package main

import (
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/sandbox"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// coreToolDefinitions builds the core built-in tool definitions.
// displaySink, if non-nil, is used by the display_file tool to emit display events;
// interactive should be true only when a TUI is active.
// sb, if non-nil and enabled, contributes the sandbox temp directory used for
// /tmp path rewriting. Sandbox command wrapping itself is resolved per tool call
// by the executor (see internal/tool/execution_pipeline.go), not here.
func coreToolDefinitions(cfg config.Config, workDir string, displaySink output.EventSink, interactive bool, handoffResponder tool.WorkflowHandoffResponder, sb *sandbox.Sandbox) []tool.ToolDef {
	var sandboxTmpDir string
	if sb != nil && sb.Enabled() {
		sandboxTmpDir = sb.TmpDir()
	}
	pp := tool.NewPathPolicyWithSandbox(workDir, cfg.Paths, sandboxTmpDir)
	excluder := tool.NewPathExcluder(cfg.Paths.ExcludePaths, cfg.Paths.ExcludePatterns)
	env := builtin.Env{
		WorkDir:                  workDir,
		PathPolicy:               &pp,
		Excluder:                 &excluder,
		EventSink:                displaySink,
		Interactive:              interactive,
		WorkflowHandoffResponder: handoffResponder,
	}
	return builtin.Builtins(env)
}

func runtimeRegistry(cfg config.Config, workDir string) *tool.Registry {
	return runtimeRegistryWithSink(cfg, workDir, nil, false, nil, nil)
}

// runtimeRegistryWithSink builds a tool registry with an optional event sink and
// interactive flag, used in interactive mode to wire the display_file tool.
func runtimeRegistryWithSink(cfg config.Config, workDir string, displaySink output.EventSink, interactive bool, handoffResponder tool.WorkflowHandoffResponder, sb *sandbox.Sandbox) *tool.Registry {
	return runtimeRegistryWithSinkAndMode(cfg, workDir, displaySink, interactive, handoffResponder, sb, nil)
}

// runtimeRegistryWithSinkAndMode builds a tool registry with optional event sink and
// interactive flag. Used in interactive mode. mgr, if non-nil, contributes MCP tool
// definitions after built-ins and config tools. Execution-mode-aware sandbox wrapping
// is resolved per tool call by the executor, not here.
func runtimeRegistryWithSinkAndMode(cfg config.Config, workDir string, displaySink output.EventSink, interactive bool, handoffResponder tool.WorkflowHandoffResponder, sb *sandbox.Sandbox, mgr *mcp.Manager) *tool.Registry {
	registry := tool.NewRegistry(coreToolDefinitions(cfg, workDir, displaySink, interactive, handoffResponder, sb)...)
	for _, def := range tool.NewRegistryFromConfig(cfg).Definitions() {
		registry.Register(def)
	}
	// Register MCP tools after built-ins and config tools.
	// MCP tools are excluded from sub-agents automatically because
	// Registry.Subset is include-list based. Ticket #6 handles deliberate exposure.
	if mgr != nil {
		for _, def := range mgr.ToolDefs() {
			registry.Register(def)
		}
	}
	return registry
}
