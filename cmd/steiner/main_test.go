package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"gopkg.in/yaml.v3"
)

func TestVersionCommandPrintsVersion(t *testing.T) {
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := stdout.String()
	if !strings.HasPrefix(got, "steiner ") {
		t.Fatalf("version output = %q, want version prefix", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestConfigCommandPrintsResolvedConfig(t *testing.T) {
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
	t.Setenv("HOME", homeDir)

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--model", "cli-model", "--verbose", "config"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var got config.Config
	if err := yaml.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal config output: %v\noutput:\n%s", err, stdout.String())
	}
	if got.Provider.Model != "cli-model" {
		t.Fatalf("provider.model = %q, want cli-model", got.Provider.Model)
	}
	if got.Provider.Parallelism != 2 {
		t.Fatalf("provider.parallelism = %d, want 2", got.Provider.Parallelism)
	}
	if got.Limits.MaxTurns != 10 {
		t.Fatalf("limits.max_turns = %d, want 10", got.Limits.MaxTurns)
	}
	if got.Logging.Level != "debug" {
		t.Fatalf("logging.level = %q, want debug", got.Logging.Level)
	}
	if got.Paths.ProjectRootOnly {
		t.Fatalf("paths.project_root_only = %v, want false", got.Paths.ProjectRootOnly)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestConfigCommandFailsForMissingExplicitConfigPath(t *testing.T) {
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "missing.yaml"), "config"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want missing config path error")
	}
	if !strings.Contains(stderr.String(), "does not exist") {
		t.Fatalf("stderr = %q, want missing-path error", stderr.String())
	}
}

func TestConfigCommandFailsForInvalidConfigContent(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "broken.yaml")
	writeFile(t, configPath, `provider:
  type: unsupported
limits:
  max_turns: 0
`)

	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--config", configPath, "config"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid config error")
	}
	if !strings.Contains(stderr.String(), "invalid config:") {
		t.Fatalf("stderr = %q, want invalid config error", stderr.String())
	}
	if !strings.Contains(stderr.String(), "provider.type") {
		t.Fatalf("stderr = %q, want provider.type validation failure", stderr.String())
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
