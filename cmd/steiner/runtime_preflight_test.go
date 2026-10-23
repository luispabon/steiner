package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/oneshot"
)

// emptyModelFlags isolates the process in a fresh project directory with an
// empty config file, so no model is configured and no developer state leaks in.
func emptyModelFlags(t *testing.T) (*cobra.Command, *cliFlags) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, "")

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetIn(strings.NewReader(""))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	return cmd, &cliFlags{configPath: configPath, trustProjectConfig: true}
}

func TestPreflightRuntimeModels(t *testing.T) {
	defined := config.Config{Models: config.ModelsConfig{
		Definitions: map[string]config.ModelConfig{"fast": {Provider: "local", ID: "fast"}},
		Effective:   config.EffectiveModelAssignments{DefaultModel: "fast"},
	}}
	activeOnly := config.Config{Models: config.ModelsConfig{
		Definitions: map[string]config.ModelConfig{"fast": {Provider: "local", ID: "fast"}},
		Effective:   config.EffectiveModelAssignments{ActiveOrchestratorModel: "fast"},
	}}

	tests := []struct {
		name        string
		cfg         config.Config
		requirement runtimeRequirement
		wantErr     error
	}{
		{name: "no requirement on empty config", cfg: config.Config{}, requirement: requireNoModel},
		{name: "orchestrator missing on empty config", cfg: config.Config{}, requirement: requireOrchestratorModel, wantErr: config.ErrNoModelConfigured},
		{name: "orchestrator from profile default", cfg: defined, requirement: requireOrchestratorModel},
		{name: "orchestrator from active override", cfg: activeOnly, requirement: requireOrchestratorModel},
		{name: "oneshot missing on empty config", cfg: config.Config{}, requirement: requireOneshotModels, wantErr: config.ErrNoModelConfigured},
		{name: "oneshot satisfied by profile default", cfg: defined, requirement: requireOneshotModels},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := preflightRuntimeModels(tt.cfg, tt.requirement); !errors.Is(err, tt.wantErr) {
				t.Fatalf("preflightRuntimeModels() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestBuildRuntimeWithRootsModelPreflight(t *testing.T) {
	cmd, flags := emptyModelFlags(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}

	t.Run("model-free runtime builds on empty config", func(t *testing.T) {
		rt, err := buildRuntimeWithRoots(context.Background(), cmd, flags, cwd, cwd, "")
		if err != nil {
			t.Fatalf("buildRuntimeWithRoots() error = %v, want nil", err)
		}
		t.Cleanup(func() { closeRuntime(&rt) })
	})

	for _, requirement := range []runtimeRequirement{requireOrchestratorModel, requireOneshotModels} {
		t.Run("rejects empty config before runtime build", func(t *testing.T) {
			flags.modelRequirement = requirement
			rt, err := buildRuntimeWithRoots(context.Background(), cmd, flags, cwd, cwd, "")
			if !errors.Is(err, config.ErrNoModelConfigured) {
				t.Fatalf("buildRuntimeWithRoots() error = %v, want %v", err, config.ErrNoModelConfigured)
			}
			if rt.closeFn != nil || rt.events != nil {
				t.Fatal("buildRuntimeWithRoots() returned a populated runtime alongside the preflight error")
			}
		})
	}
}

func TestModelCommandsRejectEmptyConfigBeforeState(t *testing.T) {
	commands := []struct {
		name string
		run  func(*cobra.Command, *cliFlags) error
	}{
		{name: "exec", run: func(cmd *cobra.Command, flags *cliFlags) error {
			return runExecMode(cmd, flags, []string{"hello"})
		}},
		{name: "interactive", run: runInteractiveMode},
		{name: "oneshot task", run: func(cmd *cobra.Command, flags *cliFlags) error {
			return runOneshotTask(cmd, flags, "task")
		}},
		{name: "oneshot resume", run: func(cmd *cobra.Command, flags *cliFlags) error {
			return runOneshotResume(cmd, flags, "run-id")
		}},
	}
	for _, tt := range commands {
		t.Run(tt.name, func(t *testing.T) {
			cmd, flags := emptyModelFlags(t)
			oldNew := newOneshotOrchestrator
			t.Cleanup(func() { newOneshotOrchestrator = oldNew })
			var orchestratorBuilds int
			newOneshotOrchestrator = func(deps oneshot.Dependencies) (oneshotOrchestrator, error) {
				orchestratorBuilds++
				return oldNew(deps)
			}

			if err := tt.run(cmd, flags); !errors.Is(err, config.ErrNoModelConfigured) {
				t.Fatalf("run error = %v, want %v", err, config.ErrNoModelConfigured)
			}
			if orchestratorBuilds != 0 {
				t.Fatalf("oneshot orchestrator built %d times, want 0", orchestratorBuilds)
			}
		})
	}
}

func TestModelFreeCommandsRunOnEmptyConfig(t *testing.T) {
	_, flags := emptyModelFlags(t)
	for _, args := range [][]string{{"tools"}, {"skills"}, {"config"}, {"models", "status"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd := newRootCommand()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(append([]string{"--config", flags.configPath}, args...))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v, want nil; output: %q", err, out.String())
			}
		})
	}
}
