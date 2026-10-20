package lsp

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

// TestManagerOptionsZeroValueDefaults checks that a zero-value ManagerOptions
// leaves the release hook and Stderr unset, and installs a no-op warning
// callback so a warning never panics on a nil function.
func TestManagerOptionsZeroValueDefaults(t *testing.T) {
	m := NewManager(config.LSPConfig{}, t.TempDir(), nil, ManagerOptions{})
	defer m.Close() //nolint:errcheck

	if m.release != nil {
		t.Error("release is set, want nil")
	}
	if m.warnFn == nil {
		t.Fatal("warnFn is nil, want a no-op callback")
	}
	m.warnFn("warn")
	if m.stderr != nil {
		t.Errorf("stderr = %v, want nil", m.stderr)
	}
}

// TestManagerOptionsZeroValueSpawnFailure checks that a spawn failure with a
// zero-value ManagerOptions returns the error instead of panicking.
func TestManagerOptionsZeroValueSpawnFailure(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.MustDuration("100ms"),
		ReadyTimeout: config.MustDuration("100ms"),
		Servers: map[string]config.LSPServerConfig{
			"missing": {
				Enabled:        true,
				Command:        "/nonexistent/lsp",
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
			},
		},
	}

	m := NewManager(cfg, tmpdir, nil, ManagerOptions{})
	defer func() { _ = m.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), managerTestTimeout)
	defer cancel()

	if _, _, err := m.entryFor(ctx, filepath.Join(tmpdir, "file.go")); err == nil {
		t.Fatal("entryFor() error = nil, want spawn error for missing binary")
	}
}

// TestManagerOptionsFieldsStored checks that each ManagerOptions field reaches
// the matching Manager field.
func TestManagerOptionsFieldsStored(t *testing.T) {
	var released, warned bool
	var stderr bytes.Buffer
	m := NewManager(config.LSPConfig{}, t.TempDir(), nil, ManagerOptions{
		Release: func(*exec.Cmd) { released = true },
		WarnFn:  func(string) { warned = true },
		Stderr:  &stderr,
	})
	defer m.Close() //nolint:errcheck

	m.release(&exec.Cmd{})
	m.warnFn("warn")
	if !released {
		t.Error("Release was not stored")
	}
	if !warned {
		t.Error("WarnFn was not stored")
	}
	if m.stderr != &stderr {
		t.Errorf("stderr = %v, want the buffer", m.stderr)
	}
}
