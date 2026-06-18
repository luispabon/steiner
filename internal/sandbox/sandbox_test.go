package sandbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestWrapCommand_Disabled(t *testing.T) {
	cfg := config.SandboxConfig{Enabled: false}
	s := New(cfg, config.PermissionsConfig{}, nil, "/workspace", "/workspace", "/home/user")

	original := exec.CommandContext(context.Background(), "echo", "hello")
	wrapped := s.WrapCommand(original)

	if wrapped != original {
		t.Error("expected WrapCommand to return original cmd unchanged when sandbox disabled")
	}
}

func TestWrapCommand_Enabled_NoBwrap(t *testing.T) {
	restore := stubSandboxHooks(t, func(string) (string, error) {
		return "", exec.ErrNotFound
	}, func(string, int) (*sshOverlay, error) {
		return nil, nil
	})
	defer restore()

	cfg := config.SandboxConfig{Enabled: true}
	s := New(cfg, config.PermissionsConfig{}, nil, "/tmp/workspace", "/tmp/workspace", "/home/user")

	original := exec.CommandContext(context.Background(), "echo", "hello")
	wrapped := s.WrapCommand(original)

	if wrapped != original {
		t.Error("expected WrapCommand to return original when bwrap is not found")
	}
}

func TestWrapCommand_Enabled_WrapsCommand(t *testing.T) {
	restore := stubSandboxHooks(t, func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	}, func(string, int) (*sshOverlay, error) {
		return nil, nil
	})
	defer restore()

	cfg := config.SandboxConfig{Enabled: true}
	s := New(cfg, config.PermissionsConfig{}, nil, "/tmp/workspace", "/tmp/workspace", "/home/user")

	original := exec.CommandContext(context.Background(), "/usr/bin/env", "FOO=bar")
	wrapped := s.WrapCommand(original)

	if wrapped == original {
		t.Fatal("expected a new wrapped cmd when sandbox enabled and bwrap present")
	}
	if wrapped.Path != "/usr/bin/bwrap" {
		t.Errorf("wrapped.Path = %q, want %q", wrapped.Path, "/usr/bin/bwrap")
	}
	if len(wrapped.Args) == 0 || wrapped.Args[0] != "bwrap" {
		t.Errorf("wrapped.Args[0] = %q, want \"bwrap\"", wrapped.Args[0])
	}

	lastSep := -1
	for i, a := range wrapped.Args {
		if a == "--" {
			lastSep = i
		}
	}
	if lastSep < 0 {
		t.Fatal(`expected "--" separator in wrapped args`)
	}

	tail := wrapped.Args[lastSep+1:]
	if len(tail) == 0 || tail[0] != "/usr/bin/env" {
		t.Errorf("expected original command after separator, got %v", tail)
	}
}

func TestWrapCommand_Enabled_InheritsStreams(t *testing.T) {
	restore := stubSandboxHooks(t, func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	}, func(string, int) (*sshOverlay, error) {
		return nil, nil
	})
	defer restore()

	cfg := config.SandboxConfig{Enabled: true}
	s := New(cfg, config.PermissionsConfig{}, nil, "/tmp/workspace", "/tmp/workspace", "/home/user")

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

func TestWrapCommand_AppendsSSHOverlayFiles(t *testing.T) {
	rootFile, err := os.CreateTemp(t.TempDir(), "overlay-*")
	if err != nil {
		t.Fatalf("create root overlay file: %v", err)
	}
	includeFile, err := os.CreateTemp(t.TempDir(), "overlay-*")
	if err != nil {
		t.Fatalf("create included overlay file: %v", err)
	}

	restore := stubSandboxHooks(t, func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	}, func(path string, childFDBase int) (*sshOverlay, error) {
		if path != sshSystemConfigPath {
			t.Fatalf("prepare path = %q, want %q", path, sshSystemConfigPath)
		}
		if childFDBase != 4 {
			t.Fatalf("childFDBase = %d, want 4", childFDBase)
		}
		return &sshOverlay{
			bwrapArgs: []string{
				"--ro-bind-data", "4", "/etc/ssh/ssh_config",
				"--ro-bind-data", "5", "/etc/ssh/ssh_config.d/10-main.conf",
			},
			memfds: []*os.File{rootFile, includeFile},
		}, nil
	})
	defer restore()

	cfg := config.SandboxConfig{Enabled: true}
	s := New(cfg, config.PermissionsConfig{}, nil, "/tmp/workspace", "/tmp/workspace", "/home/user")

	original := exec.CommandContext(context.Background(), "ssh", "example.com")
	original.ExtraFiles = []*os.File{os.Stdout}

	wrapped := s.WrapCommand(original)

	if len(wrapped.ExtraFiles) != 3 {
		t.Fatalf("extra files len = %d, want 3", len(wrapped.ExtraFiles))
	}
	if wrapped.ExtraFiles[0] != os.Stdout {
		t.Fatalf("original extra file not preserved at fd 3: %#v", wrapped.ExtraFiles)
	}
	if wrapped.ExtraFiles[1] != rootFile || wrapped.ExtraFiles[2] != includeFile {
		t.Fatalf("overlay files not appended after original ExtraFiles: %#v", wrapped.ExtraFiles)
	}
	if !containsSeq(wrapped.Args, "--ro-bind-data", "4", "/etc/ssh/ssh_config") {
		t.Fatalf("expected root overlay args, got %v", wrapped.Args)
	}
	if !containsSeq(wrapped.Args, "--ro-bind-data", "5", "/etc/ssh/ssh_config.d/10-main.conf") {
		t.Fatalf("expected included overlay args, got %v", wrapped.Args)
	}
}

func TestWrapCommand_OverlayFailureFallsBack(t *testing.T) {
	memfd, err := os.CreateTemp(t.TempDir(), "overlay-*")
	if err != nil {
		t.Fatalf("create temp overlay file: %v", err)
	}

	restore := stubSandboxHooks(t, func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	}, func(string, int) (*sshOverlay, error) {
		return &sshOverlay{memfds: []*os.File{memfd}}, errors.New("memfd unavailable")
	})
	defer restore()

	cfg := config.SandboxConfig{Enabled: true}
	s := New(cfg, config.PermissionsConfig{}, nil, "/tmp/workspace", "/tmp/workspace", "/home/user")

	original := exec.CommandContext(context.Background(), "echo", "hello")
	wrapped := s.WrapCommand(original)

	if wrapped == original {
		t.Fatal("expected sandbox wrapping to continue after overlay failure")
	}
	if len(wrapped.ExtraFiles) != 0 {
		t.Fatalf("expected no overlay files after failure, got %d", len(wrapped.ExtraFiles))
	}
	if err := memfd.Close(); err == nil {
		t.Fatal("expected failed overlay memfd to be closed")
	}
}

func TestWrapCommand_BwrapLookupFailureClosesOverlay(t *testing.T) {
	memfd, err := os.CreateTemp(t.TempDir(), "overlay-*")
	if err != nil {
		t.Fatalf("create temp overlay file: %v", err)
	}

	restore := stubSandboxHooks(t, func(string) (string, error) {
		return "", exec.ErrNotFound
	}, func(string, int) (*sshOverlay, error) {
		return &sshOverlay{memfds: []*os.File{memfd}}, nil
	})
	defer restore()

	cfg := config.SandboxConfig{Enabled: true}
	s := New(cfg, config.PermissionsConfig{}, nil, "/tmp/workspace", "/tmp/workspace", "/home/user")

	original := exec.CommandContext(context.Background(), "echo", "hello")
	wrapped := s.WrapCommand(original)

	if wrapped != original {
		t.Fatal("expected original command when bwrap lookup fails")
	}
	if err := memfd.Close(); err == nil {
		t.Fatal("expected overlay memfd to be closed when bwrap lookup fails")
	}
}

func TestWrapCommand_AllowsGitCommitInWorktreeSubdir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap integration only runs on Linux")
	}

	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not installed")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}

	repoRoot := t.TempDir()
	runGit := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(gitPath, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return string(out)
	}

	runGit(repoRoot, "init", "-b", "main")
	runGit(repoRoot, "config", "user.name", "Steiner Test")
	runGit(repoRoot, "config", "user.email", "steiner@example.com")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	runGit(repoRoot, "add", "README.md")
	runGit(repoRoot, "commit", "-m", "initial commit")

	worktreeDir := filepath.Join(repoRoot, ".git-worktrees", "step-1a")
	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0o755); err != nil {
		t.Fatalf("mkdir worktree parent: %v", err)
	}
	runGit(repoRoot, "worktree", "add", "-b", "step-1a", worktreeDir, "main")

	if err := os.WriteFile(filepath.Join(worktreeDir, "worktree.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}

	cfg := config.SandboxConfig{Enabled: true}
	s := New(cfg, config.PermissionsConfig{}, nil, repoRoot, worktreeDir, t.TempDir())
	if err := s.EnsureHome(); err != nil {
		t.Fatalf("ensure sandbox home: %v", err)
	}

	statusCmd := exec.CommandContext(context.Background(), gitPath, "status", "--short")
	statusCmd.Dir = worktreeDir
	statusWrapped := s.WrapCommand(statusCmd)
	if statusWrapped.Path != bwrapPath {
		t.Fatalf("wrapped status path = %q, want %q", statusWrapped.Path, bwrapPath)
	}
	statusOut, err := statusWrapped.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()+" "+string(statusOut)), "operation not permitted") ||
			strings.Contains(strings.ToLower(err.Error()+" "+string(statusOut)), "creating new namespace failed") {
			t.Skipf("sandbox unavailable in this environment: %v\n%s", err, statusOut)
		}
		t.Fatalf("git status failed: %v\n%s", err, statusOut)
	}
	if got := strings.TrimSpace(string(statusOut)); got != "?? worktree.txt" {
		t.Fatalf("git status output = %q, want untracked worktree file", got)
	}

	runWrappedGit := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), gitPath, args...)
		cmd.Dir = worktreeDir
		wrapped := s.WrapCommand(cmd)
		out, err := wrapped.CombinedOutput()
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()+" "+string(out)), "operation not permitted") ||
				strings.Contains(strings.ToLower(err.Error()+" "+string(out)), "creating new namespace failed") {
				t.Skipf("sandbox unavailable in this environment: %v\n%s", err, out)
			}
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	runWrappedGit("add", "worktree.txt")
	runWrappedGit("commit", "-m", "sandbox commit")
	runWrappedGit("status", "--short")
}

func TestWrapCommand_SSHOverlayIntegration_BwrapSSHConfig(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap/OpenSSH overlay integration only runs on Linux")
	}

	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not installed")
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("OpenSSH ssh not installed")
	}

	rootFile, err := os.CreateTemp(t.TempDir(), "ssh-overlay-root-*")
	if err != nil {
		t.Fatalf("create root overlay file: %v", err)
	}
	includeFile, err := os.CreateTemp(t.TempDir(), "ssh-overlay-include-*")
	if err != nil {
		t.Fatalf("create include overlay file: %v", err)
	}
	defer func() {
		_ = rootFile.Close()
		_ = includeFile.Close()
	}()
	if _, err := rootFile.WriteString("Include /etc/ssh/ssh_config.d/*.conf\nHost *\n  User overlay-test-user\n"); err != nil {
		t.Fatalf("write root overlay file: %v", err)
	}
	if _, err := includeFile.WriteString("Host *\n  Compression yes\n"); err != nil {
		t.Fatalf("write include overlay file: %v", err)
	}
	if _, err := rootFile.Seek(0, 0); err != nil {
		t.Fatalf("rewind root overlay file: %v", err)
	}
	if _, err := includeFile.Seek(0, 0); err != nil {
		t.Fatalf("rewind include overlay file: %v", err)
	}

	restore := stubSandboxHooks(t, func(string) (string, error) {
		return bwrapPath, nil
	}, func(string, int) (*sshOverlay, error) {
		return &sshOverlay{
			bwrapArgs: []string{
				"--tmpfs", "/etc/ssh/ssh_config.d",
				"--ro-bind-data", "3", "/etc/ssh/ssh_config",
				"--ro-bind-data", "4", "/etc/ssh/ssh_config.d/10-main.conf",
			},
			memfds: []*os.File{rootFile, includeFile},
		}, nil
	})
	defer restore()

	cfg := config.SandboxConfig{Enabled: true}
	rootDir := t.TempDir()
	s := New(cfg, config.PermissionsConfig{}, nil, rootDir, rootDir, t.TempDir())
	if err := s.EnsureHome(); err != nil {
		t.Fatalf("ensure sandbox home: %v", err)
	}

	ctx := context.Background()
	original := exec.CommandContext(ctx, sshPath, "-G", "github.com")
	var stdout, stderr bytes.Buffer
	original.Stdout = &stdout
	original.Stderr = &stderr

	wrapped := s.WrapCommand(original)
	if wrapped == original {
		t.Fatal("expected wrapped command when sandbox enabled and bwrap present")
	}

	if err := wrapped.Run(); err != nil {
		t.Fatalf("wrapped ssh -G failed: %v\nstderr: %s", err, stderr.String())
	}

	if strings.Contains(stderr.String(), "Bad owner or permissions") {
		t.Fatalf("stderr = %q, want no OpenSSH ownership rejection", stderr.String())
	}
	if !strings.Contains(stdout.String(), "user overlay-test-user") {
		t.Fatalf("stdout = %q, want overlay user from synthetic SSH config", stdout.String())
	}
	if !strings.Contains(stdout.String(), "compression yes") {
		t.Fatalf("stdout = %q, want overlay include from synthetic SSH config", stdout.String())
	}
}

func stubSandboxHooks(t *testing.T, lookPath func(string) (string, error), prepare func(string, int) (*sshOverlay, error)) func() {
	t.Helper()

	prevLookup := lookupBwrap
	prevPrepare := prepareSSHOverlay
	lookupBwrap = lookPath
	prepareSSHOverlay = prepare

	return func() {
		lookupBwrap = prevLookup
		prepareSSHOverlay = prevPrepare
	}
}
