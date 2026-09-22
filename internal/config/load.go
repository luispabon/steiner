package config

import (
	"fmt"
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
	// ProjectTrust gates whether the project config layer may be applied.
	// The zero value refuses an existing project config.
	ProjectTrust ProjectTrust
}

func loadEnvironment(env map[string]string) map[string]string {
	if env != nil {
		return env
	}
	return environMap(os.Environ())
}

// Load loads configuration from files, environment variables, and CLI overrides.
func Load(opts LoadOptions) (Config, error) {
	env := loadEnvironment(opts.Env)
	cfg := defaultConfig(env)

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
		if err := checkProjectTrust(item, opts.ProjectTrust); err != nil {
			return Config{}, err
		}
		patch, err := readConfigPatch(item.path, env, item.allowMissing)
		if err != nil {
			return Config{}, err
		}
		applyPatch(&cfg, patch)
		recordSandboxDisabledBy(&cfg, patch, item.project)
	}

	if err := applyEnvOverrides(&cfg, env); err != nil {
		return Config{}, err
	}
	if err := applyCLIOverrides(&cfg, opts.CLI); err != nil {
		return Config{}, err
	}
	normalizePaths(&cfg, homeDir)
	normalizeExecutionModes(&cfg)
	applyMCPDefaults(&cfg.MCP)
	if err := validate(cfg, workingDir); err != nil {
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

// checkProjectTrust refuses an existing, untrusted project config layer. A
// missing project config is always allowed; readConfigPatch handles that
// case (empty patch, or the existing "does not exist" error for an explicit
// --config path).
func checkProjectTrust(item configFilePatch, trust ProjectTrust) error {
	if !item.project || trust == ProjectTrustTrusted {
		return nil
	}
	switch _, statErr := os.Stat(item.path); {
	case statErr == nil:
		return fmt.Errorf("load project config %q: %w", item.path, ErrProjectUntrusted)
	case os.IsNotExist(statErr):
		return nil
	default:
		return fmt.Errorf("stat project config %q: %w", item.path, statErr)
	}
}

// recordSandboxDisabledBy sets cfg.Sandbox.DisabledBy to the layer that last
// toggled sandbox.enabled in patch, leaving it untouched if patch doesn't set it.
func recordSandboxDisabledBy(cfg *Config, patch configPatch, project bool) {
	if patch.Sandbox == nil || patch.Sandbox.Enabled == nil {
		return
	}
	switch {
	case *patch.Sandbox.Enabled:
		cfg.Sandbox.DisabledBy = ""
	case project:
		cfg.Sandbox.DisabledBy = SandboxDisabledByProjectConfig
	default:
		cfg.Sandbox.DisabledBy = SandboxDisabledByGlobalConfig
	}
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
