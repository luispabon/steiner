package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultConfigProjectContextFilesDefaultToNil(t *testing.T) {
	cfg := defaultConfig()

	if !reflect.DeepEqual(cfg.ProjectContext.ExtraFiles, []string(nil)) {
		t.Fatalf("project_context.extra_files = %#v, want nil", cfg.ProjectContext.ExtraFiles)
	}
	if !reflect.DeepEqual(cfg.ProjectContext.IgnoreFiles, []string(nil)) {
		t.Fatalf("project_context.ignore_files = %#v, want nil", cfg.ProjectContext.IgnoreFiles)
	}
}

func TestDefaultConfigRetryDefaults(t *testing.T) {
	cfg := defaultConfig()

	want := RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: MustDuration("250ms"),
		MaxBackoff:     MustDuration("5s"),
		RetryAfterMax:  MustDuration("30s"),
	}
	if !reflect.DeepEqual(cfg.Models["default"].Retry, want) {
		t.Fatalf("models[default].retry = %#v, want %#v", cfg.Models["default"].Retry, want)
	}
}

func TestDefaultConfigThinkingChunkDefaultsToFalse(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Logging.ThinkingChunk {
		t.Fatal("default logging.thinking_chunk = true, want false")
	}
}

func TestDefaultConfigScratchpadModeDefaultsToScaffoldOnly(t *testing.T) {
	cfg := defaultConfig()
	if got, want := cfg.ContextManagement.ScratchpadMode, ScratchpadModeScaffoldOnly; got != want {
		t.Fatalf("context_management.scratchpad_mode = %q, want %q", got, want)
	}
}

func TestDefaultConfigShowInternalScaffoldInferenceDefaultsToFalse(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Debug.ShowInternalScaffoldInference {
		t.Fatal("default debug.show_internal_scaffold_inference = true, want false")
	}
}

func TestLoadPrecedence(t *testing.T) {
	tempDir := t.TempDir()

	homeDir := filepath.Join(tempDir, "home")
	projectDir := filepath.Join(tempDir, "project")
	globalDir := filepath.Join(homeDir, ".config", "steiner")
	projectConfigDir := filepath.Join(projectDir, ".steiner")

	mustMkdirAll(t, globalDir)
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(globalDir, "config.yaml"), `scheduler:
  parallelism: 2
default_model: global
providers:
  local:
    type: openai_compat
    base_url: http://global.example/v1
models:
  global:
    provider: local
    id: global-backend
    retry:
      enabled: true
      max_attempts: 3
      initial_backoff: 250ms
      max_backoff: 5s
      retry_after_max: 30s
  env:
    provider: local
    id: env-backend
    retry:
      enabled: true
      max_attempts: 3
      initial_backoff: 250ms
      max_backoff: 5s
      retry_after_max: 30s
limits:
  max_turns: 25
approval:
  default: auto
paths:
  project_root_only: false
`)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `default_model: project
providers:
  local:
    type: openai_compat
    base_url: http://project.example/v1
models:
  project:
    provider: local
    id: project-backend
    retry:
      enabled: true
      max_attempts: 3
      initial_backoff: 250ms
      max_backoff: 5s
      retry_after_max: 30s
  cli:
    provider: local
    id: cli-backend
    retry:
      enabled: true
      max_attempts: 3
      initial_backoff: 250ms
      max_backoff: 5s
      retry_after_max: 30s
limits:
  max_turns: 10
logging:
  level: warn
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: homeDir,
		Env: map[string]string{
			"STEINER_MODEL":                 "env",
			"STEINER_MAX_TURNS":             "77",
			"STEINER_LOG_LEVEL":             "trace",
			"STEINER_SCHEDULER_PARALLELISM": "4",
		},
		CLI: CLIOverrides{
			Model:   "cli",
			Verbose: true,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.DefaultModel; got != "cli" {
		t.Fatalf("default_model = %q, want %q", got, "cli")
	}
	if got := cfg.Models["cli"].ID; got != "cli-backend" {
		t.Fatalf("models[cli].id = %q, want %q", got, "cli-backend")
	}
	if got := cfg.Scheduler.Parallelism; got != 4 {
		t.Fatalf("scheduler.parallelism = %d, want %d", got, 4)
	}
	if got := cfg.Limits.MaxTurns; got != 77 {
		t.Fatalf("limits.max_turns = %d, want %d", got, 77)
	}
	if got := cfg.Logging.Level; got != "debug" {
		t.Fatalf("logging.level = %q, want %q", got, "debug")
	}
	if got := cfg.Paths.ProjectRootOnly; got {
		t.Fatalf("paths.project_root_only = %v, want false", got)
	}
	if got := cfg.Models["global"].ID; got != "global-backend" {
		t.Fatalf("models[global].id = %q, want global config", got)
	}
	if got := cfg.Models["env"].ID; got != "env-backend" {
		t.Fatalf("models[env].id = %q, want env config", got)
	}
}

func TestLoadAppliesRetryConfigFromModelBlocks(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `default_model: default
providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default:
    provider: local
    id: qwen3-35b-a3b
    retry:
      enabled: false
      max_attempts: 5
      initial_backoff: 500ms
      max_backoff: 4s
      retry_after_max: 45s
  custom:
    provider: local
    id: custom-backend
    retry:
      enabled: true
      max_attempts: 9
      initial_backoff: 250ms
      max_backoff: 5s
      retry_after_max: 30s
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got, want := cfg.Models["default"].Retry, (RetryConfig{
		Enabled:        false,
		MaxAttempts:    5,
		InitialBackoff: MustDuration("500ms"),
		MaxBackoff:     MustDuration("4s"),
		RetryAfterMax:  MustDuration("45s"),
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("models[default].retry = %#v, want %#v", got, want)
	}
	if got, want := cfg.Models["custom"].Retry, (RetryConfig{
		Enabled:        true,
		MaxAttempts:    9,
		InitialBackoff: MustDuration("250ms"),
		MaxBackoff:     MustDuration("5s"),
		RetryAfterMax:  MustDuration("30s"),
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("models[custom].retry = %#v, want %#v", got, want)
	}
}

func TestLoadNewModelAliasesInheritRetryDefaults(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `default_model: default
providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default:
    provider: local
    id: qwen3-35b-a3b
  custom:
    provider: local
    id: custom-backend
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got, want := cfg.Models["custom"].Retry, cfg.Models["default"].Retry; !reflect.DeepEqual(got, want) {
		t.Fatalf("models[custom].retry = %#v, want %#v", got, want)
	}
}

func TestLoadPrefersExplicitHomeDirOverEnvHome(t *testing.T) {
	tempDir := t.TempDir()
	explicitHomeDir := filepath.Join(tempDir, "explicit-home")
	envHomeDir := filepath.Join(tempDir, "env-home")
	projectDir := filepath.Join(tempDir, "project")

	mustMkdirAll(t, filepath.Join(explicitHomeDir, ".config", "steiner"))
	mustMkdirAll(t, filepath.Join(envHomeDir, ".config", "steiner"))
	mustMkdirAll(t, projectDir)

	writeFile(t, filepath.Join(explicitHomeDir, ".config", "steiner", "config.yaml"), `default_model: default
providers:
  local:
    type: openai_compat
    base_url: http://explicit.example/v1
models:
  default:
    provider: local
    id: explicit-backend
`)

	writeFile(t, filepath.Join(envHomeDir, ".config", "steiner", "config.yaml"), `default_model: default
providers:
  local:
    type: openai_compat
    base_url: http://env.example/v1
models:
  default:
    provider: local
    id: env-backend
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: explicitHomeDir,
		Env: map[string]string{
			"HOME": envHomeDir,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.Providers["local"].BaseURL; got != "http://explicit.example/v1" {
		t.Fatalf("providers[local].base_url = %q, want explicit home config", got)
	}
}

func TestLoadUsesEnvHomeWhenExplicitHomeDirUnset(t *testing.T) {
	tempDir := t.TempDir()
	envHomeDir := filepath.Join(tempDir, "env-home")
	projectDir := filepath.Join(tempDir, "project")

	mustMkdirAll(t, filepath.Join(envHomeDir, ".config", "steiner"))
	mustMkdirAll(t, projectDir)
	writeFile(t, filepath.Join(envHomeDir, ".config", "steiner", "config.yaml"), `default_model: default
providers:
  local:
    type: openai_compat
    base_url: http://env.example/v1
models:
  default:
    provider: local
    id: env-backend
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		Env: map[string]string{
			"HOME": envHomeDir,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.Providers["local"].BaseURL; got != "http://env.example/v1" {
		t.Fatalf("providers[local].base_url = %q, want env home config", got)
	}
}

func TestLoadSurfacesHomeResolutionFailure(t *testing.T) {
	orig := userHomeDir
	userHomeDir = func() (string, error) {
		return "", os.ErrNotExist
	}
	t.Cleanup(func() {
		userHomeDir = orig
	})

	_, err := Load(LoadOptions{
		Env: map[string]string{
			"HOME": "",
		},
	})
	if err == nil {
		t.Fatal("Load() error = nil, want home resolution error")
	}
	if !strings.Contains(err.Error(), "resolve home directory") {
		t.Fatalf("error = %q, want home resolution failure", err)
	}
}

func TestLoadSurfacesWorkingDirResolutionFailure(t *testing.T) {
	orig := getwd
	getwd = func() (string, error) {
		return "", os.ErrNotExist
	}
	t.Cleanup(func() {
		getwd = orig
	})

	_, err := Load(LoadOptions{
		Env: map[string]string{
			"HOME": t.TempDir(),
		},
	})
	if err == nil {
		t.Fatal("Load() error = nil, want working directory resolution error")
	}
	if !strings.Contains(err.Error(), "resolve working directory") {
		t.Fatalf("error = %q, want working directory resolution failure", err)
	}
}

func TestLoadExpandsEnvInterpolation(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	homeDir := filepath.Join(tempDir, "home")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `default_model: default
providers:
  local:
    type: openai_compat
    base_url: ${STEINER_BASE_URL:-http://localhost:11434/v1}
models:
  default:
    provider: local
    id: qwen3-35b-a3b
logging:
  file: ~/.local/share/steiner/${STEINER_LOG_FILE:-steiner.log}
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: homeDir,
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.Providers["local"].BaseURL; got != "http://localhost:11434/v1" {
		t.Fatalf("providers[local].base_url = %q, want default expansion", got)
	}
	if !strings.HasPrefix(cfg.Logging.File, filepath.Join(homeDir, ".local", "share", "steiner")) {
		t.Fatalf("logging.file = %q, want home-expanded path", cfg.Logging.File)
	}
}

func TestLoadExpandsUnbracedEnvInterpolation(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	homeDir := filepath.Join(tempDir, "home")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `logging:
  file: "$HOME/steiner.log"
default_model: default
providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default:
    provider: local
    id: qwen3-35b-a3b
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", homeDir)

	cfg, err := Load(LoadOptions{
		HomeDir: homeDir,
		Env: map[string]string{
			"HOME": homeDir,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := filepath.Join(homeDir, "steiner.log")
	if got := cfg.Logging.File; got != want {
		t.Fatalf("logging.file = %q, want %q", got, want)
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: unsupported
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	_, err = Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err == nil {
		t.Fatal("Load() error = nil, want invalid config error")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %q, want provider type validation error", err)
	}
}

func TestLoadRejectsInvalidModelBlock(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `default_model: default
providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default:
    provider: local
    id: ""
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	_, err = Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err == nil {
		t.Fatal("Load() error = nil, want model id validation error")
	}
	if !strings.Contains(err.Error(), "id") {
		t.Fatalf("error = %q, want model id required error", err)
	}
}

func TestLoadRejectsRemovedAdvancedLimitFields(t *testing.T) {
	tests := []struct {
		name      string
		fieldYAML string
	}{
		{
			name:      "output_reserve",
			fieldYAML: "output_reserve: 128\n",
		},
		{
			name:      "safety_margin_tokens",
			fieldYAML: "safety_margin_tokens: 128\n",
		},
		{
			name:      "summary_max_tokens",
			fieldYAML: "summary_max_tokens: 128\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			projectDir := filepath.Join(tempDir, "project")
			projectConfigDir := filepath.Join(projectDir, ".steiner")
			mustMkdirAll(t, projectConfigDir)

			writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `default_model: default
providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default:
    provider: local
    id: qwen3-35b-a3b
    advanced:
      limits:
`+tt.fieldYAML)

			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = os.Chdir(cwd)
			})
			if err := os.Chdir(projectDir); err != nil {
				t.Fatal(err)
			}

			_, err = Load(LoadOptions{
				HomeDir: filepath.Join(tempDir, "home"),
				Env:     map[string]string{},
			})
			if err == nil {
				t.Fatal("Load() error = nil, want unknown field error")
			}
			if !strings.Contains(err.Error(), tt.name) {
				t.Fatalf("error = %q, want removed field %q in error", err, tt.name)
			}
		})
	}
}

func mustMkdirAll(t *testing.T, path string) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
}

func writeFile(t *testing.T, path, contents string) {
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}
