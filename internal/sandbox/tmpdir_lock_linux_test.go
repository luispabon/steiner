//go:build linux

package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/luispabon/steiner/internal/config"
)

func TestTmpDirLockErrorClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "busy", err: unix.EWOULDBLOCK, want: errTmpDirLockBusy},
		{name: "no locks", err: unix.ENOLCK, want: errTmpDirLockUnsupported},
		{name: "bad descriptor from filesystem", err: unix.EBADF, want: errTmpDirLockUnsupported},
		{name: "invalid argument from filesystem", err: unix.EINVAL, want: errTmpDirLockUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tmpDirLockError(tt.err); !errors.Is(got, tt.want) {
				t.Fatalf("tmpDirLockError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// openFDCount returns the number of open file descriptors for this process.
func openFDCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("read /proc/self/fd: %v", err)
	}
	return len(entries)
}

// stubTmpDirLockAcquire swaps the tmp dir lock acquisition seam.
func stubTmpDirLockAcquire(t *testing.T, fn func(string) (*os.File, error)) {
	t.Helper()
	prev := tmpDirLockAcquire
	tmpDirLockAcquire = fn
	t.Cleanup(func() { tmpDirLockAcquire = prev })
}

// swapDirPathAfterLock wraps acquireTmpDirLock so the path is replaced with a
// fresh directory immediately after the lock is taken, before the caller can
// verify the inode it locked.
func swapDirPathAfterLock(t *testing.T) func(string) (*os.File, error) {
	t.Helper()
	return func(dir string) (*os.File, error) {
		file, err := acquireTmpDirLock(dir)
		if err != nil {
			return nil, err
		}
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
		if err := os.Mkdir(dir, 0o755); err != nil {
			return nil, err
		}
		return file, nil
	}
}

// testTmpDirRoot creates a project root with the session tmp dir path reserved.
func testTmpDirRoot(t *testing.T, create bool) (root, tmpDir string) {
	t.Helper()
	root = t.TempDir()
	tmpDir = filepath.Join(root, ".steiner", "tmp", "sandbox-tmp", "session")
	if create {
		if err := os.MkdirAll(tmpDir, 0o755); err != nil {
			t.Fatalf("mkdir sandbox tmp: %v", err)
		}
	}
	return root, tmpDir
}

func TestCleanupOrphans_KeepsLockedDir(t *testing.T) {
	baseDir := t.TempDir()
	oldDir := filepath.Join(baseDir, "old")
	if err := os.Mkdir(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir old: %v", err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldDir, past, past); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}

	lock, err := acquireTmpDirLock(oldDir)
	if err != nil {
		t.Fatalf("lock old dir: %v", err)
	}

	if got := CleanupOrphans(baseDir, time.Hour); got != 0 {
		t.Fatalf("CleanupOrphans with a held lock = %d, want 0", got)
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("expected locked old dir to survive cleanup: %v", err)
	}

	if err := lock.Close(); err != nil {
		t.Fatalf("close lock: %v", err)
	}
	if got := CleanupOrphans(baseDir, time.Hour); got != 1 {
		t.Fatalf("CleanupOrphans after release = %d, want 1", got)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("expected old dir to be removed after the lock was released, stat err=%v", err)
	}
}

func TestCleanupOrphans_RemovesDirAfterHolderDies(t *testing.T) {
	baseDir := t.TempDir()
	oldDir := filepath.Join(baseDir, "crashed-session")
	if err := os.Mkdir(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}

	helper := exec.CommandContext(context.Background(), os.Args[0], "-test.run=TestTmpDirLockHelperProcess")
	helper.Env = append(os.Environ(), "STEINER_TMP_LOCK_HELPER=1", "STEINER_TMP_LOCK_HELPER_DIR="+oldDir)
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatalf("helper stdout pipe: %v", err)
	}
	helper.Stderr = os.Stderr
	if err := helper.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	exited := false
	defer func() {
		if !exited {
			_ = helper.Process.Kill()
			_ = helper.Wait()
		}
	}()

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatalf("helper readiness = %q, err=%v, want \"locked\"", line, err)
	}
	past := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(oldDir, past, past); err != nil {
		t.Fatalf("chtimes session dir: %v", err)
	}

	if got := CleanupOrphans(baseDir, 48*time.Hour); got != 0 {
		t.Fatalf("CleanupOrphans while the holder lives = %d, want 0", got)
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("expected the live session dir to survive cleanup: %v", err)
	}

	if err := helper.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	if err := helper.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("wait helper: %v", err)
		}
	}
	exited = true

	if got := CleanupOrphans(baseDir, 48*time.Hour); got != 1 {
		t.Fatalf("CleanupOrphans after the holder died = %d, want 1", got)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("expected crashed session dir to be removed, stat err=%v", err)
	}
}

// TestTmpDirLockHelperProcess holds the tmp dir lock until it is killed.
func TestTmpDirLockHelperProcess(t *testing.T) {
	if os.Getenv("STEINER_TMP_LOCK_HELPER") != "1" {
		t.Skip("helper process only")
	}
	dir := os.Getenv("STEINER_TMP_LOCK_HELPER_DIR")
	lock, err := acquireTmpDirLock(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper lock: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = lock.Close() }()
	fmt.Println("locked")
	// Block until the parent kills the process, which is what a crashed
	// runtime looks like to its peers.
	for {
		time.Sleep(time.Minute)
	}
}

func TestRemoveDirIfUnlocked_SkipsUnprobeableDirs(t *testing.T) {
	cutoff := time.Now().Add(-time.Hour)

	if removeDirIfUnlocked(filepath.Join(t.TempDir(), "missing"), cutoff) {
		t.Error("expected a missing dir to be skipped")
	}

	locked := t.TempDir()
	lock, err := acquireTmpDirLock(locked)
	if err != nil {
		t.Fatalf("lock dir: %v", err)
	}
	defer func() { _ = lock.Close() }()
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(locked, past, past); err != nil {
		t.Fatalf("chtimes locked dir: %v", err)
	}
	if removeDirIfUnlocked(locked, cutoff) {
		t.Error("expected a locked dir to be skipped")
	}
	if _, err := acquireTmpDirLock(locked); !errors.Is(err, errTmpDirLockBusy) {
		t.Errorf("second acquire error = %v, want errTmpDirLockBusy", err)
	}

	if removeDirIfUnlocked(t.TempDir(), cutoff) {
		t.Error("expected a dir newer than the cutoff to be skipped")
	}

	// An unsupported platform lock also leaves the directory alone.
	unsupported := t.TempDir()
	if err := os.Chtimes(unsupported, past, past); err != nil {
		t.Fatalf("chtimes unsupported dir: %v", err)
	}
	stubTmpDirLockAcquire(t, func(string) (*os.File, error) { return nil, errTmpDirLockUnsupported })
	if removeDirIfUnlocked(unsupported, cutoff) {
		t.Error("expected an unsupported lock probe to skip the dir")
	}
	if _, err := os.Stat(unsupported); err != nil {
		t.Errorf("expected the dir with an unsupported lock probe to remain: %v", err)
	}
}

func TestSandboxCleanup_LeavesReplacedDir(t *testing.T) {
	root, tmpDir := testTmpDirRoot(t, true)
	s := New(config.SandboxConfig{}, config.PermissionsConfig{}, root, root, "", tmpDir)
	if err := s.LockTmpDir(); err != nil {
		t.Fatalf("LockTmpDir: %v", err)
	}
	if s.tmpLock == nil {
		t.Fatal("expected a held tmp dir lock")
	}

	// Another runtime removed the directory and a replacement appeared at the
	// same path.
	if err := os.RemoveAll(tmpDir); err != nil {
		t.Fatalf("remove original dir: %v", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("create replacement dir: %v", err)
	}
	keep := filepath.Join(tmpDir, "keep")
	if err := os.WriteFile(keep, []byte("replacement"), 0o644); err != nil {
		t.Fatalf("write replacement file: %v", err)
	}

	if err := s.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("expected replacement contents to survive Cleanup: %v", err)
	}
	if s.tmpLock != nil {
		t.Error("expected the lock field to be cleared by Cleanup")
	}
	lock, err := acquireTmpDirLock(tmpDir)
	if err != nil {
		t.Fatalf("acquire replacement lock after Cleanup: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("close replacement lock: %v", err)
	}
}

func TestLockTmpDir_ClosesFDWhenInodeChanges(t *testing.T) {
	root, tmpDir := testTmpDirRoot(t, true)
	stubTmpDirLockAcquire(t, swapDirPathAfterLock(t))

	before := openFDCount(t)
	s := New(config.SandboxConfig{}, config.PermissionsConfig{}, root, root, "", tmpDir)
	if err := s.LockTmpDir(); !errors.Is(err, errTmpDirNotOwned) {
		t.Fatalf("LockTmpDir error = %v, want errTmpDirNotOwned", err)
	}
	if s.tmpLock != nil {
		t.Error("expected no adopted lock after an inode mismatch")
	}
	if after := openFDCount(t); after != before {
		t.Fatalf("open fds before = %d, after = %d: lock fd leaked", before, after)
	}
}

func TestEnsureTmpDir_ClosesFDWhenReprovisionLockFails(t *testing.T) {
	root, tmpDir := testTmpDirRoot(t, false)
	stubTmpDirLockAcquire(t, swapDirPathAfterLock(t))

	before := openFDCount(t)
	s := New(config.SandboxConfig{}, config.PermissionsConfig{}, root, root, "", tmpDir)
	bindable, err := s.ensureTmpDir()
	if !errors.Is(err, errTmpDirNotOwned) {
		t.Fatalf("ensureTmpDir error = %v, want errTmpDirNotOwned", err)
	}
	if bindable {
		t.Error("expected the dir to be unbindable after lock verification failed")
	}
	if info, statErr := os.Stat(tmpDir); statErr != nil || !info.IsDir() {
		t.Fatalf("expected the recreated dir to exist, info=%v err=%v", info, statErr)
	}
	if after := openFDCount(t); after != before {
		t.Fatalf("open fds before = %d, after = %d: lock fd leaked", before, after)
	}
}

func TestWrapCommandMode_ReprovisionsMissingTmpDir(t *testing.T) {
	restore := stubSandboxHooks(t, func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	}, func(string, int) (*sshOverlay, error) {
		return nil, nil
	})
	defer restore()

	root, tmpDir := testTmpDirRoot(t, true)
	s := New(config.SandboxConfig{Enabled: true}, config.PermissionsConfig{}, root, root, "", tmpDir)
	if err := s.LockTmpDir(); err != nil {
		t.Fatalf("LockTmpDir: %v", err)
	}
	t.Cleanup(func() { _ = s.Cleanup() })
	originalInfo, err := os.Lstat(tmpDir)
	if err != nil {
		t.Fatalf("lstat original dir: %v", err)
	}
	if err := os.RemoveAll(tmpDir); err != nil {
		t.Fatalf("remove tmp dir: %v", err)
	}

	wrapped, err := s.WrapCommandMode(exec.CommandContext(context.Background(), "echo"), false)
	if err != nil {
		t.Fatalf("WrapCommandMode: %v", err)
	}
	if !containsSeq(wrapped.Args, "--bind", tmpDir, "/tmp") {
		t.Errorf("expected --bind %s /tmp after re-provisioning, args: %v", tmpDir, wrapped.Args)
	}
	reprovisioned, err := os.Lstat(tmpDir)
	if err != nil || !reprovisioned.IsDir() {
		t.Fatalf("expected a recreated tmp dir, info=%v err=%v", reprovisioned, err)
	}
	if os.SameFile(originalInfo, reprovisioned) {
		t.Error("expected a new inode after re-provisioning")
	}
	if _, err := acquireTmpDirLock(tmpDir); !errors.Is(err, errTmpDirLockBusy) {
		t.Errorf("recreated tmp dir is not re-locked: acquire error = %v, want errTmpDirLockBusy", err)
	}
}

func TestWrapCommandMode_UnsafeTmpDirIsNotBound(t *testing.T) {
	restore := stubSandboxHooks(t, func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	}, func(string, int) (*sshOverlay, error) {
		return nil, nil
	})
	defer restore()

	root, tmpDir := testTmpDirRoot(t, false)
	if err := os.MkdirAll(filepath.Dir(tmpDir), 0o755); err != nil {
		t.Fatalf("mkdir tmp parent: %v", err)
	}
	if err := os.Symlink(t.TempDir(), tmpDir); err != nil {
		t.Fatalf("symlink tmp dir: %v", err)
	}
	s := New(config.SandboxConfig{Enabled: true}, config.PermissionsConfig{}, root, root, "", tmpDir)

	wrapped, err := s.WrapCommandMode(exec.CommandContext(context.Background(), "echo"), true)
	if err != nil {
		t.Fatalf("read-only WrapCommandMode: %v", err)
	}
	if containsSeq(wrapped.Args, "--bind", tmpDir, "/tmp") {
		t.Errorf("expected no bind of an unsafe tmp dir, args: %v", wrapped.Args)
	}
	if !containsSeq(wrapped.Args, "--tmpfs", "/tmp") {
		t.Errorf("expected tmpfs /tmp for an unsafe tmp dir, args: %v", wrapped.Args)
	}

	if _, err := s.WrapCommandMode(exec.CommandContext(context.Background(), "echo"), false); !errors.Is(err, errTmpDirNotOwned) {
		t.Errorf("build-mode WrapCommandMode error = %v, want errTmpDirNotOwned", err)
	}
}

func TestResetTmp_ReprovisionsAndRelocks(t *testing.T) {
	root, tmpDir := testTmpDirRoot(t, true)
	s := New(config.SandboxConfig{}, config.PermissionsConfig{}, root, root, "", tmpDir)
	if err := s.LockTmpDir(); err != nil {
		t.Fatalf("LockTmpDir: %v", err)
	}
	t.Cleanup(func() { _ = s.Cleanup() })
	if err := os.RemoveAll(tmpDir); err != nil {
		t.Fatalf("remove tmp dir: %v", err)
	}

	if err := s.ResetTmp(); err != nil {
		t.Fatalf("ResetTmp: %v", err)
	}
	if info, err := os.Stat(tmpDir); err != nil || !info.IsDir() {
		t.Fatalf("expected ResetTmp to recreate the tmp dir, info=%v err=%v", info, err)
	}
	if _, err := acquireTmpDirLock(tmpDir); !errors.Is(err, errTmpDirLockBusy) {
		t.Errorf("recreated tmp dir is not locked: acquire error = %v, want errTmpDirLockBusy", err)
	}
}

func TestResetTmp_KeepsLockForPresentDir(t *testing.T) {
	root, tmpDir := testTmpDirRoot(t, true)
	s := New(config.SandboxConfig{}, config.PermissionsConfig{}, root, root, "", tmpDir)
	if err := s.LockTmpDir(); err != nil {
		t.Fatalf("LockTmpDir: %v", err)
	}
	t.Cleanup(func() { _ = s.Cleanup() })
	before, err := os.Lstat(tmpDir)
	if err != nil {
		t.Fatalf("lstat tmp dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}

	if err := s.ResetTmp(); err != nil {
		t.Fatalf("ResetTmp: %v", err)
	}
	after, err := os.Lstat(tmpDir)
	if err != nil {
		t.Fatalf("lstat tmp dir after reset: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Error("expected ResetTmp to keep the same directory inode")
	}
	if entries, err := os.ReadDir(tmpDir); err != nil || len(entries) != 0 {
		t.Errorf("expected an empty tmp dir, entries=%d err=%v", len(entries), err)
	}
	if _, err := acquireTmpDirLock(tmpDir); !errors.Is(err, errTmpDirLockBusy) {
		t.Errorf("expected the dir to stay locked: acquire error = %v, want errTmpDirLockBusy", err)
	}
}

func TestEnsureTmpDir_AdoptsRecreatedDir(t *testing.T) {
	root, tmpDir := testTmpDirRoot(t, true)
	s := New(config.SandboxConfig{}, config.PermissionsConfig{}, root, root, "", tmpDir)
	if err := s.LockTmpDir(); err != nil {
		t.Fatalf("LockTmpDir: %v", err)
	}
	var messages []string
	s.SetTmpReprovisionHandler(func(message string) { messages = append(messages, message) })

	// A tool removed the tmp dir and recreated the same path with MkdirAll.
	if err := os.RemoveAll(tmpDir); err != nil {
		t.Fatalf("remove original dir: %v", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("recreate replacement dir: %v", err)
	}
	written := filepath.Join(tmpDir, "written.txt")
	if err := os.WriteFile(written, []byte("kept"), 0o644); err != nil {
		t.Fatalf("write replacement file: %v", err)
	}

	bindable, err := s.ensureTmpDir()
	if err != nil || !bindable {
		t.Fatalf("ensureTmpDir on a recreated dir = (%v, %v), want (true, nil)", bindable, err)
	}
	if _, err := os.Stat(written); err != nil {
		t.Fatalf("expected replacement contents to survive adoption: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("warning handler calls = %d (%v), want 1", len(messages), messages)
	}
	if _, err := acquireTmpDirLock(tmpDir); !errors.Is(err, errTmpDirLockBusy) {
		t.Errorf("replacement is not locked by the sandbox: acquire error = %v, want errTmpDirLockBusy", err)
	}

	if err := s.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Errorf("expected the adopted dir to be removed by Cleanup, stat err=%v", err)
	}
}

func TestWrapCommandMode_ReplacementLockedElsewhereFails(t *testing.T) {
	restore := stubSandboxHooks(t, func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	}, func(string, int) (*sshOverlay, error) {
		return nil, nil
	})
	defer restore()

	root, tmpDir := testTmpDirRoot(t, true)
	s := New(config.SandboxConfig{Enabled: true}, config.PermissionsConfig{}, root, root, "", tmpDir)
	if err := s.LockTmpDir(); err != nil {
		t.Fatalf("LockTmpDir: %v", err)
	}
	t.Cleanup(func() { _ = s.Cleanup() })
	if err := os.RemoveAll(tmpDir); err != nil {
		t.Fatalf("remove original dir: %v", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("recreate replacement dir: %v", err)
	}
	other, err := acquireTmpDirLock(tmpDir)
	if err != nil {
		t.Fatalf("lock replacement: %v", err)
	}
	defer func() { _ = other.Close() }()

	if _, err := s.WrapCommandMode(exec.CommandContext(context.Background(), "echo"), false); !errors.Is(err, errTmpDirLockBusy) {
		t.Errorf("build-mode error = %v, want errTmpDirLockBusy", err)
	}

	wrapped, err := s.WrapCommandMode(exec.CommandContext(context.Background(), "echo"), true)
	if err != nil {
		t.Fatalf("read-only WrapCommandMode: %v", err)
	}
	if containsSeq(wrapped.Args, "--bind", tmpDir, "/tmp") {
		t.Errorf("expected no bind of a busy replacement, args: %v", wrapped.Args)
	}
	if !containsSeq(wrapped.Args, "--tmpfs", "/tmp") {
		t.Errorf("expected tmpfs /tmp when the replacement is busy, args: %v", wrapped.Args)
	}
}

func TestResetTmp_AdoptsUnlockedReplacement(t *testing.T) {
	root, tmpDir := testTmpDirRoot(t, true)
	s := New(config.SandboxConfig{}, config.PermissionsConfig{}, root, root, "", tmpDir)
	if err := s.LockTmpDir(); err != nil {
		t.Fatalf("LockTmpDir: %v", err)
	}
	t.Cleanup(func() { _ = s.Cleanup() })
	if err := os.RemoveAll(tmpDir); err != nil {
		t.Fatalf("remove original dir: %v", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("recreate replacement dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "stale"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	if err := s.ResetTmp(); err != nil {
		t.Fatalf("ResetTmp: %v", err)
	}
	if entries, err := os.ReadDir(tmpDir); err != nil || len(entries) != 0 {
		t.Errorf("expected the adopted dir to be emptied, entries=%d err=%v", len(entries), err)
	}
	if _, err := acquireTmpDirLock(tmpDir); !errors.Is(err, errTmpDirLockBusy) {
		t.Errorf("adopted dir is not locked: acquire error = %v, want errTmpDirLockBusy", err)
	}
}
