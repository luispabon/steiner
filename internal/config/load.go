package config

import (
	"os"
)

var (
	userHomeDir = os.UserHomeDir
	getwd       = os.Getwd
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

	env := loadEnvironment(opts.Env)

	homeDir, err := resolveHomeDir(opts.HomeDir, env)
	if err != nil {
		return Config{}, err
	}
	workingDir, err := resolveWorkingDir(opts.WorkingDir)
	if err != nil {
		return Config{}, err
	}

	paths := resolveConfigPaths(opts, homeDir, workingDir)
	if err := applyConfigFilePatches(&cfg, env, paths); err != nil {
		return Config{}, err
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
