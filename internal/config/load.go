package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// CLIOverrides contains command-line override values.
type CLIOverrides struct {
	ConfigPath string
	Model      string
	Verbose    bool
}

// LoadOptions contains options for loading configuration.
type LoadOptions struct {
	GlobalConfigPath  string
	ProjectConfigPath string
	WorkingDir        string
	HomeDir           string
	Env               map[string]string
	CLI               CLIOverrides
}

// Load loads configuration from files, environment variables, and CLI overrides.
func Load(opts LoadOptions) (Config, error) {
	cfg := defaultConfig()

	env := opts.Env
	if env == nil {
		env = environMap(os.Environ())
	}

	homeDir := opts.HomeDir
	if homeDir == "" {
		homeDir = os.Getenv("HOME")
		if homeDir == "" {
			if resolved, err := os.UserHomeDir(); err == nil {
				homeDir = resolved
			}
		}
	}

	workingDir := opts.WorkingDir
	if workingDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			workingDir = cwd
		}
	}

	globalPath := opts.GlobalConfigPath
	if globalPath == "" {
		if homeDir != "" {
			globalPath = filepath.Join(homeDir, ".config", "steiner", "config.yaml")
		}
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

	for _, item := range []struct {
		path         string
		allowMissing bool
	}{
		{path: globalPath, allowMissing: true},
		{path: projectPath, allowMissing: !explicitProjectPath},
	} {
		if item.path == "" {
			continue
		}
		patch, err := readConfigPatch(item.path, env, item.allowMissing)
		if err != nil {
			return Config{}, err
		}
		applyPatch(&cfg, patch)
	}

	if err := applyEnvOverrides(&cfg, env); err != nil {
		return Config{}, err
	}
	applyCLIOverrides(&cfg, opts.CLI)
	normalizePaths(&cfg, homeDir)
	if err := validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// readConfigPatch reads a config file and returns it as a patch.
func readConfigPatch(path string, env map[string]string, allowMissing bool) (configPatch, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if allowMissing {
				return configPatch{}, nil
			}
			return configPatch{}, fmt.Errorf("config path %q does not exist", path)
		}
		return configPatch{}, fmt.Errorf("read config %q: %w", path, err)
	}

	expanded := expandEnvText(string(contents), func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	})

	// First decode into a raw mapping to check if model is a scalar alias.
	// We can't use KnownFields here since the node decode won't enforce it.
	var rawMapping yaml.Node
	if err := yaml.Unmarshal([]byte(expanded), &rawMapping); err != nil {
		return configPatch{}, fmt.Errorf("parse config %q: %w", path, err)
	}

	// The root node from Unmarshal is a DocumentNode; the actual mapping is
	// in Content[0].
	var root *yaml.Node
	if rawMapping.Kind == yaml.DocumentNode && len(rawMapping.Content) > 0 {
		root = rawMapping.Content[0]
	} else {
		root = &rawMapping
	}

	var patch configPatch
	if root.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(root.Content); i += 2 {
			key := root.Content[i]
			if key.Value == "model" && root.Content[i+1].Kind == yaml.ScalarNode {
				patch.ModelAlias = root.Content[i+1].Value
				root.Content[i+1].Kind = yaml.ScalarNode
				root.Content[i+1].Tag = "!!null"
				root.Content[i+1].Value = ""
				break
			}
		}
	}
	// Re-marshal to bytes so we can decode with KnownFields.
	cleaned, err := yaml.Marshal(&rawMapping)
	if err != nil {
		return configPatch{}, fmt.Errorf("marshal cleaned config %q: %w", path, err)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(cleaned)))
	dec.KnownFields(true)
	if err := dec.Decode(&patch); err != nil {
		return configPatch{}, fmt.Errorf("parse config %q: %w", path, err)
	}

	return patch, nil
}

// applyCLIOverrides applies command-line overrides to the config.
func applyCLIOverrides(cfg *Config, cli CLIOverrides) {
	if cli.Model != "" {
		if m, ok := cfg.Models[cli.Model]; ok {
			cfg.Model = m
		}
	}
	if cli.Verbose {
		cfg.Logging.Level = "debug"
	}
}

// normalizePaths expands ~ to home directory in path fields.
func normalizePaths(cfg *Config, homeDir string) {
	expand := func(path string) string {
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

	cfg.Logging.File = expand(cfg.Logging.File)

	for i := range cfg.ProjectContext.ExtraFiles {
		cfg.ProjectContext.ExtraFiles[i] = expand(cfg.ProjectContext.ExtraFiles[i])
	}
	for i := range cfg.ProjectContext.IgnoreFiles {
		cfg.ProjectContext.IgnoreFiles[i] = expand(cfg.ProjectContext.IgnoreFiles[i])
	}
	for i := range cfg.Paths.WritablePaths {
		cfg.Paths.WritablePaths[i] = expand(cfg.Paths.WritablePaths[i])
	}
	for i := range cfg.Paths.BlockedPaths {
		cfg.Paths.BlockedPaths[i] = expand(cfg.Paths.BlockedPaths[i])
	}
	for name, tool := range cfg.Tools {
		tool.Exec = expand(tool.Exec)
		cfg.Tools[name] = tool
	}
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
