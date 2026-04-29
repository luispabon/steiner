package main

import (
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// coreToolDefinitions builds the core built-in tool definitions.
// displaySink, if non-nil, is used by the display_file tool to emit display events;
// interactive should be true only when a TUI is active.
func coreToolDefinitions(cfg config.Config, workDir string, displaySink output.EventSink, interactive bool) []tool.ToolDef {
	pp := tool.NewPathPolicy(workDir, cfg.Paths)
	excluder := tool.NewPathExcluder(cfg.Paths.ExcludePaths, cfg.Paths.ExcludePatterns)
	env := builtin.Env{
		WorkDir:     workDir,
		PathPolicy:  &pp,
		Excluder:    &excluder,
		EventSink:   displaySink,
		Interactive: interactive,
	}
	return builtin.Builtins(env)
}

func runtimeRegistry(cfg config.Config, workDir string) (*tool.Registry, error) {
	return runtimeRegistryWithSink(cfg, workDir, nil, false)
}

// runtimeRegistryWithSink builds a tool registry with an optional event sink and
// interactive flag, used in interactive mode to wire the display_file tool.
func runtimeRegistryWithSink(cfg config.Config, workDir string, displaySink output.EventSink, interactive bool) (*tool.Registry, error) {
	registry := tool.NewRegistry(coreToolDefinitions(cfg, workDir, displaySink, interactive)...)
	for _, def := range tool.NewRegistryFromConfig(cfg).Definitions() {
		registry.Register(def)
	}
	return registry, nil
}
