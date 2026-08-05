package main

import (
	"os/exec"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/sandbox"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// modeGetter returns the current execution mode.
type modeGetter interface {
	Mode() config.ExecutionMode
}

// coreToolDefinitions builds the core built-in tool definitions.
// displaySink, if non-nil, is used by the display_file tool to emit display events;
// interactive should be true only when a TUI is active.
// sb, if non-nil, provides a CommandWrapper for the bash tool.
// modeGtr, if non-nil, is used by the command wrapper to determine sandbox readOnlyProject setting.
func coreToolDefinitions(cfg config.Config, workDir string, displaySink output.EventSink, interactive bool, handoffResponder tool.WorkflowHandoffResponder, sb *sandbox.Sandbox, modeGtr modeGetter) []tool.ToolDef {
	var sandboxTmpDir string
	if sb != nil && sb.Enabled() {
		sandboxTmpDir = sb.TmpDir()
	}
	pp := tool.NewPathPolicyWithSandbox(workDir, cfg.Paths, sandboxTmpDir)
	excluder := tool.NewPathExcluder(cfg.Paths.ExcludePaths, cfg.Paths.ExcludePatterns)
	var commandWrapper func(*exec.Cmd) *exec.Cmd
	if sb != nil {
		if modeGtr != nil {
			commandWrapper = func(cmd *exec.Cmd) *exec.Cmd {
				return sb.WrapCommandMode(cmd, modeGtr.Mode() == config.ExecutionModePlan)
			}
		} else {
			commandWrapper = sb.WrapCommand
		}
	}
	env := builtin.Env{
		WorkDir:                  workDir,
		PathPolicy:               &pp,
		Excluder:                 &excluder,
		EventSink:                displaySink,
		Interactive:              interactive,
		WorkflowHandoffResponder: handoffResponder,
		CommandWrapper:           commandWrapper,
	}
	return builtin.Builtins(env)
}

func runtimeRegistry(cfg config.Config, workDir string) *tool.Registry {
	return runtimeRegistryWithSink(cfg, workDir, nil, false, nil, nil)
}

// runtimeRegistryWithSink builds a tool registry with an optional event sink and
// interactive flag, used in interactive mode to wire the display_file tool.
// modeGtr, if non-nil, is used by the command wrapper to determine sandbox readOnlyProject setting.
func runtimeRegistryWithSink(cfg config.Config, workDir string, displaySink output.EventSink, interactive bool, handoffResponder tool.WorkflowHandoffResponder, sb *sandbox.Sandbox) *tool.Registry {
	return runtimeRegistryWithSinkAndMode(cfg, workDir, displaySink, interactive, handoffResponder, sb, nil, nil)
}

// runtimeRegistryWithSinkAndMode builds a tool registry with optional event sink, interactive flag,
// and mode getter for sandbox-aware command wrapping. Used in interactive mode with mode-aware sandboxing.
// mgr, if non-nil, contributes MCP tool definitions after built-ins and config tools.
func runtimeRegistryWithSinkAndMode(cfg config.Config, workDir string, displaySink output.EventSink, interactive bool, handoffResponder tool.WorkflowHandoffResponder, sb *sandbox.Sandbox, modeGtr modeGetter, mgr *mcp.Manager) *tool.Registry {
	registry := tool.NewRegistry(coreToolDefinitions(cfg, workDir, displaySink, interactive, handoffResponder, sb, modeGtr)...)
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
