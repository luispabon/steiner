package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestWrapCommand_OverlayResourcesReleaseAfterStart(t *testing.T) {
	bwrap := filepath.Join(t.TempDir(), "bwrap")
	if err := os.WriteFile(bwrap, []byte("#!/bin/sh\nwhile [ \"$1\" != \"--\" ]; do shift; done\nshift\nexec \"$@\"\n"), 0o755); err != nil {
		t.Fatalf("write bwrap stub: %v", err)
	}

	callerFile, err := os.CreateTemp(t.TempDir(), "caller-*")
	if err != nil {
		t.Fatalf("create caller file: %v", err)
	}
	t.Cleanup(func() { _ = callerFile.Close() })
	if _, err := callerFile.WriteString("caller\n"); err != nil {
		t.Fatalf("write caller file: %v", err)
	}
	if _, err := callerFile.Seek(0, 0); err != nil {
		t.Fatalf("rewind caller file: %v", err)
	}
	overlayFile, err := os.CreateTemp(t.TempDir(), "overlay-*")
	if err != nil {
		t.Fatalf("create overlay file: %v", err)
	}
	t.Cleanup(func() { _ = overlayFile.Close() })
	if _, err := overlayFile.WriteString("overlay\n"); err != nil {
		t.Fatalf("write overlay file: %v", err)
	}
	if _, err := overlayFile.Seek(0, 0); err != nil {
		t.Fatalf("rewind overlay file: %v", err)
	}

	restore := stubSandboxHooks(t, func(string) (string, error) { return bwrap, nil }, func(string, int) (*sshOverlay, error) {
		return &sshOverlay{
			bwrapArgs: []string{"--ro-bind-data", "4", "/etc/ssh/ssh_config"},
			memfds:    []*os.File{overlayFile},
		}, nil
	})
	defer restore()

	s := New(config.SandboxConfig{Enabled: true}, config.PermissionsConfig{}, t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir())
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", "cat <&3; cat <&4")
	cmd.ExtraFiles = []*os.File{callerFile}
	var output strings.Builder
	cmd.Stdout = &output
	wrapped := s.WrapCommand(cmd)
	if err := wrapped.Start(); err != nil {
		t.Fatalf("start wrapped command: %v", err)
	}
	s.ReleaseCommandResources(wrapped)
	if err := wrapped.Wait(); err != nil {
		t.Fatalf("wait wrapped command: %v", err)
	}
	if got, want := output.String(), "caller\noverlay\n"; got != want {
		t.Fatalf("child output = %q, want %q", got, want)
	}
	if _, err := callerFile.WriteString("still caller-owned"); err != nil {
		t.Fatalf("caller ExtraFiles was closed: %v", err)
	}
	if _, err := overlayFile.WriteString("closed"); err == nil {
		t.Fatal("overlay file remained open after command start")
	}
}
