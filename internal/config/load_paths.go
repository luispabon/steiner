package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type configFilePatch struct {
	path         string
	allowMissing bool
}

func loadEnvironment(env map[string]string) map[string]string {
	if env != nil {
		return env
	}
	return environMap(os.Environ())
}

func resolveHomeDir(explicitHome string, env map[string]string) (string, error) {
	if explicitHome != "" {
		return explicitHome, nil
	}
	if envHome := env["HOME"]; envHome != "" {
		return envHome, nil
	}
	resolved, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return resolved, nil
}

func resolveWorkingDir(explicitWorkingDir string) (string, error) {
	if explicitWorkingDir != "" {
		return explicitWorkingDir, nil
	}
	cwd, err := getwd()
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	return cwd, nil
}

func resolveConfigPaths(opts LoadOptions, homeDir, workingDir string) []configFilePatch {
	globalPath := opts.GlobalConfigPath
	if globalPath == "" && homeDir != "" {
		globalPath = filepath.Join(homeDir, ".config", "steiner", "config.yaml")
	}

	projectPath := opts.ProjectConfigPath
	explicitProjectPath := false
	if opts.CLI.ConfigPath != "" {
		projectPath = opts.CLI.ConfigPath
		explicitProjectPath = true
	}
	if projectPath == "" && workingDir != "" {
		projectPath = filepath.Join(workingDir, ".steiner", "config.yaml")
	}

	return []configFilePatch{
		{path: globalPath, allowMissing: true},
		{path: projectPath, allowMissing: !explicitProjectPath},
	}
}

func applyConfigFilePatches(cfg *Config, env map[string]string, patches []configFilePatch) error {
	for _, item := range patches {
		if item.path == "" {
			continue
		}
		patch, err := readConfigPatch(item.path, env, item.allowMissing)
		if err != nil {
			return err
		}
		applyPatch(cfg, patch)
	}
	return nil
}
