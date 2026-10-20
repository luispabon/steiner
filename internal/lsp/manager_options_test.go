package lsp

import (
	"bytes"
	"os/exec"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

// TestManagerOptionsZeroValueDefaults checks that a zero-value ManagerOptions
// leaves the release hook, warning callback, and Stderr unset, the same as the
// no-option NewManager call before the options struct.
func TestManagerOptionsZeroValueDefaults(t *testing.T) {
	m := NewManager(config.LSPConfig{}, t.TempDir(), nil, ManagerOptions{})
	defer m.Close() //nolint:errcheck

	if m.release != nil {
		t.Error("release is set, want nil")
	}
	if m.warnFn != nil {
		t.Error("warnFn is set, want nil")
	}
	if m.stderr != nil {
		t.Errorf("stderr = %v, want nil", m.stderr)
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
