package main

import (
	"os/exec"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/sandbox"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// coreToolDefinitions builds the core built-in tool definitions.
// displaySink, if non-nil, is used by the display_file tool to emit display events;
// interactive should be true only when a TUI is active.
// sb, if non-nil, provides a CommandWrapper for the bash tool.
func coreToolDefinitions(cfg config.Config, workDir string, displaySink output.EventSink, interactive bool, handoffResponder tool.WorkflowHandoffResponder, sb *sandbox.Sandbox) []tool.ToolDef {
	var sandboxTmpDir string
	if sb != nil && sb.Enabled() {
		sandboxTmpDir = sb.TmpDir()
	}
	pp := tool.NewPathPolicyWithSandbox(workDir, cfg.Paths, sandboxTmpDir)
	excluder := tool.NewPathExcluder(cfg.Paths.ExcludePaths, cfg.Paths.ExcludePatterns)
	var commandWrapper func(*exec.Cmd) *exec.Cmd
	if sb != nil {
		commandWrapper = sb.WrapCommand
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
func runtimeRegistryWithSink(cfg config.Config, workDir string, displaySink output.EventSink, interactive bool, handoffResponder tool.WorkflowHandoffResponder, sb *sandbox.Sandbox) *tool.Registry {
	registry := tool.NewRegistry(coreToolDefinitions(cfg, workDir, displaySink, interactive, handoffResponder, sb)...)
	for _, def := range tool.NewRegistryFromConfig(cfg).Definitions() {
		registry.Register(def)
	}
	return registry
}
