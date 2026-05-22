package config

import (
	"path/filepath"
	"strings"
)

// applyCLIOverrides applies command-line overrides to the config.
func applyCLIOverrides(cfg *Config, cli CLIOverrides) {
	if cli.Model != "" {
		if _, ok := cfg.Models[cli.Model]; ok {
			cfg.DefaultModel = cli.Model
		}
	}
	if cli.Verbose {
		cfg.Logging.Level = "debug"
	}
	if cli.ContextMode != "" {
		cfg.ContextManagement.Mode = cli.ContextMode
	}
	if cli.CavemanMode != nil {
		cfg.CavemanMode = *cli.CavemanMode
	}
}

// normalizePaths expands ~ to home directory in path fields.
func normalizePaths(cfg *Config, homeDir string) {
	cfg.Logging.File = expandHomePath(cfg.Logging.File, homeDir)

	for i := range cfg.ProjectContext.ExtraFiles {
		cfg.ProjectContext.ExtraFiles[i] = expandHomePath(cfg.ProjectContext.ExtraFiles[i], homeDir)
	}
	for i := range cfg.ProjectContext.IgnoreFiles {
		cfg.ProjectContext.IgnoreFiles[i] = expandHomePath(cfg.ProjectContext.IgnoreFiles[i], homeDir)
	}
	for i := range cfg.Paths.WritablePaths {
		cfg.Paths.WritablePaths[i] = expandHomePath(cfg.Paths.WritablePaths[i], homeDir)
	}
	for i := range cfg.Paths.BlockedPaths {
		cfg.Paths.BlockedPaths[i] = expandHomePath(cfg.Paths.BlockedPaths[i], homeDir)
	}
	for i := range cfg.Paths.ExcludePaths {
		cfg.Paths.ExcludePaths[i] = expandHomePath(cfg.Paths.ExcludePaths[i], homeDir)
	}
	for name, tool := range cfg.Tools {
		tool.Exec = expandHomePath(tool.Exec, homeDir)
		cfg.Tools[name] = tool
	}
}

func expandHomePath(path, homeDir string) string {
	if path == "" || !strings.HasPrefix(path, "~") {
		return path
	}
	if path == "~" {
		return homeDir
	}
	if strings.HasPrefix(path, "~/") && homeDir != "" {
		return filepath.Join(homeDir, path[2:])
	}
	return path
}

// environMap converts a slice of environment variables (KEY=VALUE) to a map.
func environMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		out[key] = value
	}
	return out
}
