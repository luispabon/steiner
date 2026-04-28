package main

import (
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func coreToolDefinitions(cfg config.Config, workDir string) []tool.ToolDef {
	pp := tool.NewPathPolicy(workDir, cfg.Paths)
	excluder := tool.NewPathExcluder(cfg.Paths.ExcludePaths, cfg.Paths.ExcludePatterns)
	env := builtin.Env{
		WorkDir:    workDir,
		PathPolicy: &pp,
		Excluder:   &excluder,
	}
	return builtin.Builtins(env)
}

func runtimeRegistry(cfg config.Config, workDir string) (*tool.Registry, error) {
	registry := tool.NewRegistry(coreToolDefinitions(cfg, workDir)...)
	for _, def := range tool.NewRegistryFromConfig(cfg).Definitions() {
		registry.Register(def)
	}
	return registry, nil
}
