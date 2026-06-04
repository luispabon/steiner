package sandbox

import (
	"context"
	"os/exec"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestWrapCommand_Disabled(t *testing.T) {
	cfg := config.SandboxConfig{Enabled: false}
	s := New(cfg, config.PermissionsConfig{}, nil, "/workspace", "/home/user")

	original := exec.CommandContext(context.Background(), "echo", "hello")
	wrapped := s.WrapCommand(original)

	if wrapped != original {
		t.Error("expected WrapCommand to return original cmd unchanged when sandbox disabled")
	}
}

func TestWrapCommand_Enabled_NoBwrap(t *testing.T) {
	// When bwrap is not on PATH, WrapCommand should return original unchanged.
	// We simulate this by using a path we control; if bwrap happens to exist
	// the test falls through to the wrapping path.
	cfg := config.SandboxConfig{Enabled: true}
	s := New(cfg, config.PermissionsConfig{}, nil, "/tmp/workspace", "/home/user")

	_, err := exec.LookPath("bwrap")
	if err != nil {
		// bwrap not installed — WrapCommand should return original.
		original := exec.CommandContext(context.Background(), "echo", "hello")
		wrapped := s.WrapCommand(original)
		if wrapped != original {
			t.Error("expected WrapCommand to return original when bwrap is not found")
		}
		return
	}

	// bwrap is installed — verify wrapping produces correct Args structure.
	original := exec.CommandContext(context.Background(), "/usr/bin/env", "FOO=bar")
	wrapped := s.WrapCommand(original)

	if wrapped == original {
		t.Fatal("expected a new wrapped cmd when sandbox enabled and bwrap present")
	}

	bwrapPath, _ := exec.LookPath("bwrap")
	if wrapped.Path != bwrapPath {
		t.Errorf("wrapped.Path = %q, want %q", wrapped.Path, bwrapPath)
	}

	// First arg should be "bwrap".
	if len(wrapped.Args) == 0 || wrapped.Args[0] != "bwrap" {
		t.Errorf("wrapped.Args[0] = %q, want \"bwrap\"", wrapped.Args[0])
	}

	// Separator "--" must be present.
	separatorFound := false
	for _, a := range wrapped.Args {
		if a == "--" {
			separatorFound = true
			break
		}
	}
	if !separatorFound {
		t.Error("expected \"--\" separator in wrapped Args")
	}

	// Original command args must appear after "--".
	lastSep := -1
	for i, a := range wrapped.Args {
		if a == "--" {
			lastSep = i
		}
	}
	tail := wrapped.Args[lastSep+1:]
	if len(tail) == 0 || tail[0] != "/usr/bin/env" {
		t.Errorf("expected original command after \"--\", got %v", tail)
	}
}

func TestWrapCommand_Enabled_InheritsStreams(t *testing.T) {
	_, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not installed")
	}

	cfg := config.SandboxConfig{Enabled: true}
	s := New(cfg, config.PermissionsConfig{}, nil, "/tmp/workspace", "/home/user")

	original := exec.CommandContext(context.Background(), "ls")

	wrapped := s.WrapCommand(original)

	if wrapped.Stdin != original.Stdin {
		t.Error("Stdin not inherited")
	}
	if wrapped.Stdout != original.Stdout {
		t.Error("Stdout not inherited")
	}
	if wrapped.Stderr != original.Stderr {
		t.Error("Stderr not inherited")
	}
	if wrapped.Dir != "" {
		t.Errorf("Dir = %q, want empty string (CWD handled by --chdir)", wrapped.Dir)
	}
}
