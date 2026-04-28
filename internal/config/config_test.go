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
model: global
models:
  global:
    type: openai_compat
    base_url: http://global.example/v1
    model: global-backend
    max_completion_tokens: 2048
    context_size: 8192
    compaction:
      safety_margin_tokens: 256
      summary_max_tokens: 128
  env:
    type: openai_compat
    base_url: http://env.example/v1
    model: env-backend
    max_completion_tokens: 1024
    context_size: 16384
    compaction:
      safety_margin_tokens: 512
      summary_max_tokens: 256
limits:
  max_turns: 25
approval:
  default: auto
paths:
  project_root_only: false
`)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `model: project
models:
  project:
    type: openai_compat
    base_url: http://project.example/v1
    model: project-backend
    max_completion_tokens: 4096
    context_size: 32768
    compaction:
      safety_margin_tokens: 1024
      summary_max_tokens: 512
  cli:
    type: openai_compat
    base_url: http://cli.example/v1
    model: cli-backend
    max_completion_tokens: 8192
    context_size: 65536
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 1024
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

	if got := cfg.Model; got != "cli" {
		t.Fatalf("model = %q, want %q", got, "cli")
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
	if got := cfg.Models["cli"].BaseURL; got != "http://cli.example/v1" {
		t.Fatalf("models[cli].base_url = %q, want cli override", got)
	}
	if got := cfg.Models["cli"].Model; got != "cli-backend" {
		t.Fatalf("models[cli].model = %q, want cli-backend", got)
	}
	if got := cfg.Models["global"].BaseURL; got != "http://global.example/v1" {
		t.Fatalf("models[global].base_url = %q, want global config", got)
	}
	if got := cfg.Models["env"].Model; got != "env-backend" {
		t.Fatalf("models[env].model = %q, want env config", got)
	}
}

func TestLoadExpandsEnvInterpolation(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	homeDir := filepath.Join(tempDir, "home")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `model: default
models:
  default:
    type: openai_compat
    base_url: ${STEINER_BASE_URL:-http://localhost:11434/v1}
    model: qwen3-35b-a3b
    max_completion_tokens: 8192
    context_size: 32768
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 1024
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

	if got := cfg.Models["default"].BaseURL; got != "http://localhost:11434/v1" {
		t.Fatalf("models[default].base_url = %q, want default expansion", got)
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
model: default
models:
  default:
    type: openai_compat
    base_url: http://localhost:11434/v1
    model: qwen3-35b-a3b
    max_completion_tokens: 8192
    context_size: 32768
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 1024
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

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `provider:
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
	if !strings.Contains(err.Error(), "provider") {
		t.Fatalf("error = %q, want old provider schema rejection", err)
	}
}

func TestLoadRejectsUnknownModelAlias(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `model: missing
models:
  default:
    type: openai_compat
    base_url: http://localhost:11434/v1
    model: qwen3-35b-a3b
    max_completion_tokens: 8192
    context_size: 32768
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 1024
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
		t.Fatal("Load() error = nil, want missing model alias error")
	}
	if !strings.Contains(err.Error(), "model \"missing\" is not defined") {
		t.Fatalf("error = %q, want alias validation", err)
	}
}

func TestLoadAllowsZeroMaxTurns(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `model: default
models:
  default:
    type: openai_compat
    base_url: http://localhost:11434/v1
    model: qwen3-35b-a3b
    max_completion_tokens: 8192
    context_size: 32768
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 1024
limits:
  max_turns: 0
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
	if got := cfg.Limits.MaxTurns; got != 0 {
		t.Fatalf("limits.max_turns = %d, want 0", got)
	}
}

func TestLoadRejectsSummaryMaxTokensAboveMaxCompletionTokens(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `model: default
models:
  default:
    type: openai_compat
    base_url: http://localhost:11434/v1
    model: qwen3-35b-a3b
    max_completion_tokens: 256
    context_size: 32768
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 512
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
		t.Fatal("Load() error = nil, want summary max validation error")
	}
	if !strings.Contains(err.Error(), `models["default"].compaction.summary_max_tokens must be less than or equal to models["default"].max_completion_tokens`) {
		t.Fatalf("error = %q, want summary/max completion validation", err)
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMergesExtraParams(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `model: default
models:
  default:
    type: openai_compat
    base_url: http://localhost:11434/v1
    model: qwen3-35b-a3b
    extra_params:
      temperature: 0.7
      top_p: 0.9
    max_completion_tokens: 8192
    context_size: 32768
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 1024
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

	ep := cfg.Models["default"].ExtraParams
	if ep == nil {
		t.Fatal("ExtraParams = nil, want non-nil map")
	}
	if got, want := ep["temperature"], 0.7; got != want {
		t.Fatalf("ExtraParams[temperature] = %v, want %v", got, want)
	}
	if got, want := ep["top_p"], 0.9; got != want {
		t.Fatalf("ExtraParams[top_p] = %v, want %v", got, want)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
