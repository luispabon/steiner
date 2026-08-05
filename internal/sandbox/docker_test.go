package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestDockerDenyArgs(t *testing.T) {
	tests := []struct {
		name       string
		candidates func(t *testing.T) []string
		wantMask   bool
	}{
		{
			name: "existing socket is masked",
			candidates: func(t *testing.T) []string {
				sock := filepath.Join(t.TempDir(), "docker.sock")
				if err := os.WriteFile(sock, nil, 0o644); err != nil {
					t.Fatalf("create fake socket: %v", err)
				}
				return []string{sock}
			},
			wantMask: true,
		},
		{
			name: "missing candidate emits no mask",
			candidates: func(t *testing.T) []string {
				return []string{filepath.Join(t.TempDir(), "does-not-exist.sock")}
			},
			wantMask: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidates := tc.candidates(t)
			args := dockerDenyArgs(candidates)

			if !slices.Contains(args, "--unsetenv") || !containsSeq(args, "--unsetenv", "DOCKER_HOST") {
				t.Errorf("expected --unsetenv DOCKER_HOST in args: %v", args)
			}

			hasMask := slices.Contains(args, "--bind")
			if hasMask != tc.wantMask {
				t.Errorf("mask present = %v, want %v; args: %v", hasMask, tc.wantMask, args)
			}
		})
	}
}

func TestDockerDenyArgs_SymlinkedParentResolves(t *testing.T) {
	realDir := t.TempDir()
	sock := filepath.Join(realDir, "docker.sock")
	if err := os.WriteFile(sock, nil, 0o644); err != nil {
		t.Fatalf("create fake socket: %v", err)
	}

	linkDir := filepath.Join(t.TempDir(), "run-link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatalf("symlink parent dir: %v", err)
	}
	candidate := filepath.Join(linkDir, "docker.sock")

	args := dockerDenyArgs([]string{candidate})

	if !containsSeq(args, "--bind", "/dev/null", sock) {
		t.Errorf("expected mask resolved to real path %s in args: %v", sock, args)
	}
	if containsSeq(args, "--bind", "/dev/null", candidate) {
		t.Errorf("did not expect mask to use the symlinked candidate path directly: %v", args)
	}
}

func TestDockerDenyArgs_DuplicateCandidatesCollapse(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "docker.sock")
	if err := os.WriteFile(sock, nil, 0o644); err != nil {
		t.Fatalf("create fake socket: %v", err)
	}

	args := dockerDenyArgs([]string{sock, sock})

	count := 0
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--bind" && args[i+1] == "/dev/null" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one mask for duplicate candidates, got %d: %v", count, args)
	}
}

func TestBuildArgs_Docker_DeniedMasksExistingSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "docker.sock")
	if err := os.WriteFile(sock, nil, 0o644); err != nil {
		t.Fatalf("create fake socket: %v", err)
	}
	restore := stubDockerSocketCandidates(t, []string{sock})
	defer restore()

	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "/home/user", nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{Docker: false})

	if !containsSeq(args, "--bind", "/dev/null", sock) {
		t.Errorf("expected docker socket mask in args: %v", args)
	}
	if !containsSeq(args, "--unsetenv", "DOCKER_HOST") {
		t.Errorf("expected --unsetenv DOCKER_HOST in args: %v", args)
	}
}

func TestBuildArgs_Docker_AllowedEmitsNothing(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "docker.sock")
	if err := os.WriteFile(sock, nil, 0o644); err != nil {
		t.Fatalf("create fake socket: %v", err)
	}
	restore := stubDockerSocketCandidates(t, []string{sock})
	defer restore()

	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "/home/user", nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{Docker: true})

	if containsSeq(args, "--bind", "/dev/null", sock) {
		t.Errorf("did not expect docker socket mask when permitted: %v", args)
	}
	if containsSeq(args, "--unsetenv", "DOCKER_HOST") {
		t.Errorf("did not expect --unsetenv DOCKER_HOST when permitted: %v", args)
	}
}

func TestBuildArgs_Docker_MaskOrderedAfterMountsBeforeChdir(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "docker.sock")
	if err := os.WriteFile(sock, nil, 0o644); err != nil {
		t.Fatalf("create fake socket: %v", err)
	}
	restore := stubDockerSocketCandidates(t, []string{sock})
	defer restore()

	hostMounts := []config.HostMount{{Path: "/data/rw", Mode: "rw"}}
	overlayArgs := []string{"--tmpfs", "/etc/ssh/ssh_config.d"}

	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "/home/user", hostMounts, overlayArgs, "/tmp/sandbox-tmp", false, config.PermissionsConfig{Docker: false})

	hostMountIdx := slices.Index(args, "/data/rw")
	overlayIdx := slices.Index(args, "--tmpfs")
	maskIdx := -1
	for i := 0; i+2 < len(args); i++ {
		if args[i] == "--bind" && args[i+1] == "/dev/null" {
			maskIdx = i
			break
		}
	}
	chdirIdx := slices.Index(args, "--chdir")

	if hostMountIdx < 0 || overlayIdx < 0 || maskIdx < 0 || chdirIdx < 0 {
		t.Fatalf("expected all markers present: hostMountIdx=%d overlayIdx=%d maskIdx=%d chdirIdx=%d args=%v", hostMountIdx, overlayIdx, maskIdx, chdirIdx, args)
	}
	if maskIdx < hostMountIdx || maskIdx < overlayIdx {
		t.Errorf("expected docker mask after host mounts and overlay args: %v", args)
	}
	if maskIdx > chdirIdx {
		t.Errorf("expected docker mask before --chdir: %v", args)
	}
}

func stubDockerSocketCandidates(t *testing.T, candidates []string) func() {
	t.Helper()
	prev := dockerSocketCandidates
	dockerSocketCandidates = func() []string { return candidates }
	return func() {
		dockerSocketCandidates = prev
	}
}
