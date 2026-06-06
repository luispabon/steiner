package sandbox

import (
	"os"
	"testing"
)

func TestSSHOverlayClose_NilAndEmpty(t *testing.T) {
	var overlay *sshOverlay
	if err := overlay.Close(); err != nil {
		t.Fatalf("nil overlay close: %v", err)
	}

	if err := (&sshOverlay{}).Close(); err != nil {
		t.Fatalf("empty overlay close: %v", err)
	}
}

func TestSSHOverlayClose_ClosesMemfds(t *testing.T) {
	f1, err := os.CreateTemp(t.TempDir(), "ssh-overlay-*")
	if err != nil {
		t.Fatalf("create temp file 1: %v", err)
	}
	f2, err := os.CreateTemp(t.TempDir(), "ssh-overlay-*")
	if err != nil {
		t.Fatalf("create temp file 2: %v", err)
	}

	overlay := &sshOverlay{
		memfds: []*os.File{f1, nil, f2},
	}

	if err := overlay.Close(); err != nil {
		t.Fatalf("close overlay: %v", err)
	}

	if err := f1.Close(); err == nil {
		t.Fatal("expected first memfd to be closed")
	}
	if err := f2.Close(); err == nil {
		t.Fatal("expected second memfd to be closed")
	}
}

func TestSSHOverlayScaffoldFields(t *testing.T) {
	file := sshOverlayFile{
		sourcePath:  "/etc/ssh/ssh_config",
		sandboxPath: "/tmp/ssh_config",
		content:     []byte("Host *"),
	}
	resolution := sshIncludeResolution{
		files:              []sshOverlayFile{file},
		replacementDirs:    []string{"/tmp"},
		skippedDiagnostics: []string{"skipped include"},
	}
	overlay := sshOverlay{
		bwrapArgs: []string{"--ro-bind", "/etc/ssh/ssh_config", "/tmp/ssh_config"},
		memfds:    nil,
	}

	if got, want := sshSystemConfigPath, "/etc/ssh/ssh_config"; got != want {
		t.Fatalf("sshSystemConfigPath = %q, want %q", got, want)
	}
	if got, want := sshMaxIncludeDepth, 8; got != want {
		t.Fatalf("sshMaxIncludeDepth = %d, want %d", got, want)
	}
	if got, want := sshMaxFiles, 128; got != want {
		t.Fatalf("sshMaxFiles = %d, want %d", got, want)
	}
	if got, want := sshMaxFileBytes, 1<<20; got != want {
		t.Fatalf("sshMaxFileBytes = %d, want %d", got, want)
	}
	if got, want := sshMaxTotalBytes, 8<<20; got != want {
		t.Fatalf("sshMaxTotalBytes = %d, want %d", got, want)
	}
	if len(overlay.bwrapArgs) != 3 {
		t.Fatalf("overlay.bwrapArgs len = %d, want 3", len(overlay.bwrapArgs))
	}
	if len(resolution.files) != 1 || len(resolution.replacementDirs) != 1 || len(resolution.skippedDiagnostics) != 1 {
		t.Fatalf("unexpected resolution scaffold: %+v", resolution)
	}
	if resolution.files[0].sourcePath != file.sourcePath || resolution.files[0].sandboxPath != file.sandboxPath {
		t.Fatalf("resolution file mismatch: %+v", resolution.files[0])
	}
}
