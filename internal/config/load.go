package config

import (
	"os"
	"time"
)

var (
	userHomeDir = os.UserHomeDir
	getwd       = os.Getwd
)

// CLIOverrides contains command-line override values.
type CLIOverrides struct {
	ConfigPath string
	Model      string
	Profile    string
	Verbose    bool
	Unsafe     bool
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

func loadEnvironment(env map[string]string) map[string]string {
	if env != nil {
		return env
	}
	return environMap(os.Environ())
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
	for _, item := range paths {
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
	normalizeExecutionModes(&cfg)
	applyMCPDefaults(&cfg.MCP)
	if err := validate(cfg); err != nil {
		return Config{}, err
	}

	effective, err := ResolveEffectiveAssignments(&cfg, cfg.Selection.Profile)
	if err != nil {
		return Config{}, err
	}
	if cfg.Selection.ModelOverride != "" {
		effective.ActiveOrchestratorModel = cfg.Selection.ModelOverride
	}
	cfg.Models.Effective = effective

	return cfg, nil
}

func normalizeExecutionModes(cfg *Config) {
	if cfg.Modes.Default == "" {
		cfg.Modes.Default = ExecutionModeBuild
	}
}

// defaultMCPConnectTimeout is the default per-server MCP connect timeout used
// when a server omits connect_timeout (or sets it to zero). 15s mirrors
// crush's default (issue #409 range 5-30s).
const defaultMCPConnectTimeout = 15 * time.Second

// applyMCPDefaults normalizes per-server MCP defaults after patching.
func applyMCPDefaults(cfg *MCPConfig) {
	for name, srv := range cfg.Servers {
		if srv.Transport == "" {
			srv.Transport = "stdio"
			cfg.Servers[name] = srv
		}
		if srv.Approval == "" {
			srv.Approval = "ask"
			cfg.Servers[name] = srv
		}
		if srv.ConnectTimeout.IsZero() {
			srv.ConnectTimeout = Duration{value: int64(defaultMCPConnectTimeout), set: true}
			cfg.Servers[name] = srv
		}
	}
}
