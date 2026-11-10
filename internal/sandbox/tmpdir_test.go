package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestEnsureTmpDir_WarnsOnceWhenReprovisioned(t *testing.T) {
	root := t.TempDir()
	tmpDir := filepath.Join(root, ".steiner", "tmp", "sandbox-tmp", "session")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("mkdir sandbox tmp: %v", err)
	}
	s := New(config.SandboxConfig{}, config.PermissionsConfig{}, root, root, "", tmpDir)
	var messages []string
	s.SetTmpReprovisionHandler(func(message string) { messages = append(messages, message) })

	if bindable, err := s.ensureTmpDir(); err != nil || !bindable {
		t.Fatalf("ensureTmpDir on a present dir = (%v, %v), want (true, nil)", bindable, err)
	}
	if len(messages) != 0 {
		t.Fatalf("handler fired without a re-provision: %v", messages)
	}

	if err := os.RemoveAll(tmpDir); err != nil {
		t.Fatalf("remove tmp dir: %v", err)
	}
	if bindable, err := s.ensureTmpDir(); err != nil || !bindable {
		t.Fatalf("ensureTmpDir after loss = (%v, %v), want (true, nil)", bindable, err)
	}
	if err := os.RemoveAll(tmpDir); err != nil {
		t.Fatalf("remove tmp dir again: %v", err)
	}
	if bindable, err := s.ensureTmpDir(); err != nil || !bindable {
		t.Fatalf("ensureTmpDir after second loss = (%v, %v), want (true, nil)", bindable, err)
	}

	if len(messages) != 1 {
		t.Fatalf("handler calls = %d (%v), want exactly 1", len(messages), messages)
	}
	if messages[0] != tmpReprovisionMessage {
		t.Errorf("handler message = %q, want %q", messages[0], tmpReprovisionMessage)
	}
}

func TestEnsureTmpDir_UnsupportedLockStillBinds(t *testing.T) {
	root := t.TempDir()
	tmpDir := filepath.Join(root, ".steiner", "tmp", "sandbox-tmp", "session")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("mkdir sandbox tmp: %v", err)
	}
	prev := tmpDirLockAcquire
	tmpDirLockAcquire = func(string) (*os.File, error) { return nil, errTmpDirLockUnsupported }
	t.Cleanup(func() { tmpDirLockAcquire = prev })

	s := New(config.SandboxConfig{}, config.PermissionsConfig{}, root, root, "", tmpDir)
	if err := s.LockTmpDir(); err != nil {
		t.Fatalf("LockTmpDir with unsupported locking: %v", err)
	}
	bindable, err := s.ensureTmpDir()
	if err != nil || !bindable {
		t.Fatalf("ensureTmpDir with unsupported locking = (%v, %v), want (true, nil)", bindable, err)
	}
	if !s.tmpLockUnsupported || s.tmpLock != nil {
		t.Fatalf("expected unprotected state, got unsupported=%v lock=%v", s.tmpLockUnsupported, s.tmpLock)
	}

	if err := s.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Errorf("expected the unprotected tmp dir to be removed by Cleanup, stat err=%v", err)
	}
}

func TestWrapAndResetAfterCleanupReturnClosed(t *testing.T) {
	restore := stubSandboxHooks(t, func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	}, func(string, int) (*sshOverlay, error) {
		return nil, nil
	})
	defer restore()

	root := t.TempDir()
	tmpDir := filepath.Join(root, ".steiner", "tmp", "sandbox-tmp", "session")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("mkdir sandbox tmp: %v", err)
	}
	s := New(config.SandboxConfig{Enabled: true}, config.PermissionsConfig{}, root, root, "", tmpDir)
	if err := s.LockTmpDir(); err != nil {
		t.Fatalf("LockTmpDir: %v", err)
	}
	if err := s.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if err := s.Cleanup(); err != nil {
		t.Fatalf("second Cleanup must be a no-op, got: %v", err)
	}

	if _, err := s.WrapCommandMode(exec.CommandContext(context.Background(), "echo"), false); !errors.Is(err, errSandboxClosed) {
		t.Errorf("WrapCommandMode after Cleanup error = %v, want errSandboxClosed", err)
	}
	if err := s.ResetTmp(); !errors.Is(err, errSandboxClosed) {
		t.Errorf("ResetTmp after Cleanup error = %v, want errSandboxClosed", err)
	}
	if err := s.LockTmpDir(); !errors.Is(err, errSandboxClosed) {
		t.Errorf("LockTmpDir after Cleanup error = %v, want errSandboxClosed", err)
	}
}
