package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
)

func TestLoadAcceptsCanonicalSubAgentTypes(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	var subAgents strings.Builder
	for _, agentType := range delegation.AllAgentTypes() {
		fmt.Fprintf(&subAgents, "    %s: default\n", agentType)
	}
	writeValidationSyncConfig(t, configPath, fmt.Sprintf("models:\n  sub_agents:\n%s", subAgents.String()))

	if _, err := config.Load(config.LoadOptions{
		HomeDir:    filepath.Join(tempDir, "home"),
		WorkingDir: tempDir,
		Env:        map[string]string{},
		CLI:        config.CLIOverrides{ConfigPath: configPath},
	}); err != nil {
		t.Fatalf("config.Load() error = %v, want nil", err)
	}
}

func TestLoadRejectsUnknownSubAgentType(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	writeValidationSyncConfig(t, configPath, "models:\n  sub_agents:\n    bogus: default\n")

	_, err := config.Load(config.LoadOptions{
		HomeDir:    filepath.Join(tempDir, "home"),
		WorkingDir: tempDir,
		Env:        map[string]string{},
		CLI:        config.CLIOverrides{ConfigPath: configPath},
	})
	if err == nil {
		t.Fatal("config.Load() error = nil, want unknown sub-agent type error")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("config.Load() error = %v, want error containing bogus", err)
	}
}

func writeValidationSyncConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
}
