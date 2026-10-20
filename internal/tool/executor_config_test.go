package tool

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestExecutorConfigFromConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		cfg       config.Config
		wantLimit int
		wantPaths config.PathsConfig
	}{
		{
			name:      "zero config applies default output cap and empty paths",
			cfg:       config.Config{},
			wantLimit: 65536,
		},
		{
			name:      "negative output cap falls back to default",
			cfg:       config.Config{Limits: config.LimitsConfig{ToolOutputMaxBytes: -1}},
			wantLimit: 65536,
		},
		{
			name:      "configured output cap is kept",
			cfg:       config.Config{Limits: config.LimitsConfig{ToolOutputMaxBytes: 48}},
			wantLimit: 48,
		},
		{
			name: "paths are projected",
			cfg: config.Config{Paths: config.PathsConfig{
				ProjectRootOnly: true,
				WritablePaths:   []string{"out"},
				BlockedPaths:    []string{"secrets"},
				ExcludePaths:    []string{"vendor"},
				ExcludePatterns: []string{"*.log"},
			}},
			wantLimit: 65536,
			wantPaths: config.PathsConfig{
				ProjectRootOnly: true,
				WritablePaths:   []string{"out"},
				BlockedPaths:    []string{"secrets"},
				ExcludePaths:    []string{"vendor"},
				ExcludePatterns: []string{"*.log"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ExecutorConfigFromConfig(tt.cfg)
			if got.outputLimit != tt.wantLimit {
				t.Errorf("outputLimit = %d, want %d", got.outputLimit, tt.wantLimit)
			}
			if !reflect.DeepEqual(got.paths, tt.wantPaths) {
				t.Errorf("paths = %+v, want %+v", got.paths, tt.wantPaths)
			}
		})
	}
}

func TestExecutorConfigFromConfigClonesPaths(t *testing.T) {
	t.Parallel()
	blocked := []string{"secrets"}
	got := ExecutorConfigFromConfig(config.Config{Paths: config.PathsConfig{BlockedPaths: blocked}})
	blocked[0] = "changed"
	if got.paths.BlockedPaths[0] != "secrets" {
		t.Errorf("BlockedPaths[0] = %q after caller mutation, want %q: projection shares the config backing array", got.paths.BlockedPaths[0], "secrets")
	}
}

func TestNewExecutorWithConfigZeroValueUsesDefaultOutputLimit(t *testing.T) {
	t.Parallel()
	exec := NewExecutorWithConfig(NewRegistry(), ExecutorConfig{}, nil, t.TempDir(), "", Unsandboxed{})
	if exec.outputLimit != 65536 {
		t.Errorf("outputLimit = %d, want 65536 for the zero ExecutorConfig", exec.outputLimit)
	}
}

func TestNewExecutorWithConfigMatchesNewExecutor(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	cfg := config.Config{
		Limits: config.LimitsConfig{ToolOutputMaxBytes: 48},
		Paths: config.PathsConfig{
			ProjectRootOnly: true,
			BlockedPaths:    []string{filepath.Join(workDir, "secrets")},
		},
	}
	registry := NewRegistry(ToolDef{
		Name:    "read",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil },
	})
	legacy := NewExecutor(registry, cfg, nil, workDir, "", Unsandboxed{})
	projected := NewExecutorWithConfig(registry, ExecutorConfigFromConfig(cfg), nil, workDir, "", Unsandboxed{})

	if legacy.outputLimit != projected.outputLimit {
		t.Errorf("outputLimit: NewExecutor = %d, NewExecutorWithConfig = %d", legacy.outputLimit, projected.outputLimit)
	}
	if legacy.workDir != projected.workDir {
		t.Errorf("workDir: NewExecutor = %q, NewExecutorWithConfig = %q", legacy.workDir, projected.workDir)
	}
	if !reflect.DeepEqual(legacy.pathPolicy, projected.pathPolicy) {
		t.Errorf("pathPolicy: NewExecutor = %+v, NewExecutorWithConfig = %+v", legacy.pathPolicy, projected.pathPolicy)
	}

	for _, tc := range []struct {
		name string
		exec *Executor
	}{
		{name: "NewExecutor", exec: legacy},
		{name: "NewExecutorWithConfig", exec: projected},
	} {
		_, err := tc.exec.Execute(context.Background(), "read", "", map[string]any{"path": filepath.Join(workDir, "secrets", "key.txt")})
		var toolErr *ToolExecutionError
		if !errors.As(err, &toolErr) || toolErr.Kind != "policy_denied" {
			t.Errorf("%s: Execute(read blocked path) error = %v, want policy_denied", tc.name, err)
		}
	}
}
