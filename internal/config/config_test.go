package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPrecedence(t *testing.T) {
	tempDir := t.TempDir()

	homeDir := filepath.Join(tempDir, "home")
	projectDir := filepath.Join(tempDir, "project")
	globalDir := filepath.Join(homeDir, ".config", "steiner")
	projectConfigDir := filepath.Join(projectDir, ".steiner")

	mustMkdirAll(t, globalDir)
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(globalDir, "config.yaml"), `provider:
  model: global-model
  parallelism: 2
limits:
  max_turns: 25
approval:
  default: auto
paths:
  project_root_only: false
`)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `provider:
  model: project-model
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
			"STEINER_MODEL":                "env-model",
			"STEINER_MAX_TURNS":            "77",
			"STEINER_LOG_LEVEL":            "trace",
			"STEINER_PROVIDER_PARALLELISM": "4",
		},
		CLI: CLIOverrides{
			Model:   "cli-model",
			Verbose: true,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.Provider.Model; got != "cli-model" {
		t.Fatalf("provider.model = %q, want %q", got, "cli-model")
	}
	if got := cfg.Provider.Parallelism; got != 4 {
		t.Fatalf("provider.parallelism = %d, want %d", got, 4)
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
	if got := cfg.Provider.BaseURL; got != "http://localhost:11434/v1" {
		t.Fatalf("provider.base_url = %q, want default", got)
	}
}

func TestLoadExpandsEnvInterpolation(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `provider:
  base_url: ${STEINER_BASE_URL:-http://localhost:11434/v1}
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

	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.Provider.BaseURL; got != "http://localhost:11434/v1" {
		t.Fatalf("provider.base_url = %q, want default expansion", got)
	}
	if !strings.HasPrefix(cfg.Logging.File, filepath.Join(os.Getenv("HOME"), ".local", "share", "steiner")) {
		t.Fatalf("logging.file = %q, want home-expanded path", cfg.Logging.File)
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `provider:
  type: unsupported
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

	_, err = Load(LoadOptions{})
	if err == nil {
		t.Fatal("Load() error = nil, want invalid config error")
	}
	if !strings.Contains(err.Error(), "provider.type") {
		t.Fatalf("error = %q, want provider.type validation", err)
	}
	if !strings.Contains(err.Error(), "limits.max_turns") {
		t.Fatalf("error = %q, want limits.max_turns validation", err)
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
