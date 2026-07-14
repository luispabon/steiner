package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestBuildArgs_WorkspaceBind(t *testing.T) {
	args := BuildArgs("/my/workspace", "/my/workspace", "/my/workspace/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, nil, "/tmp/sandbox-tmp", false)

	if !containsSeq(args, "--bind", "/my/workspace", "/my/workspace") {
		t.Errorf("expected --bind /my/workspace /my/workspace in args: %v", args)
	}
}

func TestBuildArgs_SeparateWritableRootAndWorkDir(t *testing.T) {
	args := BuildArgs("/repo", "/repo/.git-worktrees/step-1a", "/repo/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, nil, "/tmp/sandbox-tmp", false)

	if !containsSeq(args, "--bind", "/repo", "/repo") {
		t.Fatalf("expected writable root bind for /repo in args: %v", args)
	}
	if !containsSeq(args, "--chdir", "/repo/.git-worktrees/step-1a") {
		t.Fatalf("expected chdir to workDir in args: %v", args)
	}
	if containsSeq(args, "--bind", "/repo/.git-worktrees/step-1a", "/repo/.git-worktrees/step-1a") {
		t.Fatalf("did not expect workDir to be mounted writable directly: %v", args)
	}
}

func TestBuildArgs_SandboxHomeBind(t *testing.T) {
	sandboxHome := "/my/workspace/.steiner/home"
	args := BuildArgs("/my/workspace", "/my/workspace", sandboxHome, "/home/user",
		config.PermissionsConfig{}, nil, nil, "/tmp/sandbox-tmp", false)

	if !containsSeq(args, "--bind", sandboxHome, sandboxHome) {
		t.Errorf("expected --bind %s %s in args: %v", sandboxHome, sandboxHome, args)
	}
}

func TestBuildArgs_TmpBind(t *testing.T) {
	args := BuildArgs("/my/workspace", "/my/workspace", "/my/workspace/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, nil, "/tmp/sandbox-tmp", false)

	if !containsSeq(args, "--bind", "/tmp/sandbox-tmp", "/tmp") {
		t.Errorf("expected --bind /tmp/sandbox-tmp /tmp in args: %v", args)
	}
}

func TestBuildArgs_UnshareAll(t *testing.T) {
	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, nil, "/tmp/sandbox-tmp", false)

	if !slices.Contains(args, "--unshare-all") {
		t.Errorf("expected --unshare-all in args: %v", args)
	}
	if !slices.Contains(args, "--share-net") {
		t.Errorf("expected --share-net in args: %v", args)
	}
}

func TestBuildArgs_NoSetenvHome(t *testing.T) {
	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, nil, "/tmp/sandbox-tmp", false)

	if containsSeq(args, "--setenv", "HOME") {
		t.Errorf("--setenv HOME should not be present in args: %v", args)
	}
}

func TestBuildArgs_RoBindRoot(t *testing.T) {
	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, nil, "/tmp/sandbox-tmp", false)

	if !containsSeq(args, "--ro-bind", "/", "/") {
		t.Errorf("expected --ro-bind / / in args: %v", args)
	}

	// Must be first mount (after unshare flags)
	rbIdx := slices.Index(args, "--ro-bind")
	bindIdx := slices.Index(args, "--bind")
	if bindIdx >= 0 && rbIdx > bindIdx {
		t.Errorf("--ro-bind / / must appear before any --bind, rbIdx=%d bindIdx=%d", rbIdx, bindIdx)
	}
}

func TestBuildArgs_Chdir(t *testing.T) {
	args := BuildArgs("/my/workspace", "/my/workspace", "/my/workspace/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, nil, "/tmp/sandbox-tmp", false)

	if !containsSeq(args, "--chdir", "/my/workspace") {
		t.Errorf("expected --chdir /my/workspace in args: %v", args)
	}
}

func TestBuildArgs_HostMountsAppended(t *testing.T) {
	hostMounts := []config.HostMount{
		{Path: "/data/ro", Mode: "ro"},
		{Path: "/data/rw", Mode: "rw"},
	}
	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "/home/user",
		config.PermissionsConfig{}, hostMounts, nil, "/tmp/sandbox-tmp", false)

	if !containsSeq(args, "--ro-bind", "/data/ro", "/data/ro") {
		t.Errorf("expected --ro-bind /data/ro /data/ro in args: %v", args)
	}
	if !containsSeq(args, "--bind", "/data/rw", "/data/rw") {
		t.Errorf("expected --bind /data/rw /data/rw in args: %v", args)
	}
}

func TestBuildArgs_CacheDirBind(t *testing.T) {
	home := t.TempDir()
	cacheDir := filepath.Join(home, ".cache")
	if err := os.Mkdir(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}

	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", home,
		config.PermissionsConfig{}, nil, nil, "/tmp/sandbox-tmp", false)

	if !containsSeq(args, "--bind", cacheDir, cacheDir) {
		t.Errorf("expected --bind %s %s in args: %v", cacheDir, cacheDir, args)
	}
}

func TestBuildArgs_CacheDirBindResolvesSymlink(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "cache")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target cache dir: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".cache")); err != nil {
		t.Fatalf("symlink cache dir: %v", err)
	}

	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", home,
		config.PermissionsConfig{}, nil, nil, "/tmp/sandbox-tmp", false)

	if !containsSeq(args, "--bind", target, target) {
		t.Errorf("expected resolved cache bind %s %s in args: %v", target, target, args)
	}
	if containsSeq(args, "--bind", filepath.Join(home, ".cache"), filepath.Join(home, ".cache")) {
		t.Errorf("expected symlink cache path not to be used as bind destination: %v", args)
	}
}

func TestBuildArgs_NoCacheDirWhenMissing(t *testing.T) {
	home := t.TempDir()
	cacheDir := filepath.Join(home, ".cache")

	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", home,
		config.PermissionsConfig{}, nil, nil, "/tmp/sandbox-tmp", false)

	if containsSeq(args, "--bind", cacheDir, cacheDir) {
		t.Errorf("should not mount missing cache dir: %v", args)
	}
}

func TestBuildArgs_NoCacheDirWhenNoHome(t *testing.T) {
	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "",
		config.PermissionsConfig{}, nil, nil, "/tmp/sandbox-tmp", false)

	if containsSeq(args, "--bind", "/.cache", "/.cache") {
		t.Errorf("should not mount /.cache when userHome is empty: %v", args)
	}
}

func TestBuildArgs_AppendsOverlayBeforeChdir(t *testing.T) {
	overlayArgs := []string{
		"--tmpfs", "/etc/ssh/ssh_config.d",
		"--ro-bind-data", "3", "/etc/ssh/ssh_config",
	}

	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, overlayArgs, "/tmp/sandbox-tmp", false)

	if !containsSeq(args, overlayArgs...) {
		t.Fatalf("expected overlay args to be present: %v", args)
	}

	overlayIdx := slices.Index(args, "--ro-bind-data")
	chdirIdx := slices.Index(args, "--chdir")
	if overlayIdx < 0 || chdirIdx < 0 || overlayIdx > chdirIdx {
		t.Fatalf("expected overlay mounts before --chdir, args=%v", args)
	}
}

func TestBuildArgs_ReadOnlyProject_RoBindsRootAndBindsSteiner(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".steiner"), 0o755); err != nil {
		t.Fatalf("mkdir .steiner: %v", err)
	}

	args := BuildArgs(root, root, filepath.Join(root, ".steiner", "home"), "/home/user",
		config.PermissionsConfig{}, nil, nil, "/tmp/sandbox-tmp", true)

	steinerPath := filepath.Join(root, ".steiner")
	if !containsSeq(args, "--ro-bind", root, root, "--bind", steinerPath, steinerPath) {
		t.Errorf("expected --ro-bind %s %s followed by --bind %s %s in args: %v", root, root, steinerPath, steinerPath, args)
	}

	if containsSeq(args, "--bind", root, root) {
		t.Errorf("expected --bind %s %s to be absent when readOnlyProject=true: %v", root, root, args)
	}
}

func TestBuildArgs_ReadOnlyProject_False_UnchangedFromBefore(t *testing.T) {
	root := "/test/root"
	args := BuildArgs(root, root, filepath.Join(root, ".steiner", "home"), "/home/user",
		config.PermissionsConfig{}, nil, nil, "/tmp/sandbox-tmp", false)

	if !containsSeq(args, "--bind", root, root) {
		t.Errorf("expected --bind %s %s in args when readOnlyProject=false: %v", root, root, args)
	}

	steinerPath := filepath.Join(root, ".steiner")
	if containsSeq(args, "--bind", steinerPath, steinerPath) {
		t.Errorf("should not have .steiner bind when readOnlyProject=false: %v", args)
	}

	if containsSeq(args, "--ro-bind", root, root) {
		t.Errorf("should not have --ro-bind for root when readOnlyProject=false: %v", args)
	}
}

// containsSeq returns true if needle appears as a contiguous subsequence in haystack.
func containsSeq(haystack []string, needle ...string) bool {
	if len(needle) == 0 {
		return true
	}
outer:
	for i := 0; i+len(needle) <= len(haystack); i++ {
		for j, n := range needle {
			if haystack[i+j] != n {
				continue outer
			}
		}
		return true
	}
	return false
}
