package main

import (
	"os"
	"path/filepath"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func coreToolDefinitions(cfg config.Config, execPath string) []tool.ToolDef {
	pp := tool.NewPathPolicy(filepath.Dir(execPath), cfg.Paths)
	excluder := tool.NewPathExcluder(cfg.Paths.ExcludePaths, cfg.Paths.ExcludePatterns)
	env := builtin.Env{
		WorkDir:    filepath.Dir(execPath),
		PathPolicy: &pp,
		Excluder:   &excluder,
	}
	return builtin.Builtins(env)
}

func runtimeRegistry(cfg config.Config) (*tool.Registry, error) {
	execPath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	registry := tool.NewRegistry(coreToolDefinitions(cfg, execPath)...)
	for _, def := range tool.NewRegistryFromConfig(cfg).Definitions() {
		registry.Register(def)
	}
	return registry, nil
}
