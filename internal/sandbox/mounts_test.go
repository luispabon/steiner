package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestBuildArgs_WorkspaceBind(t *testing.T) {
	args := BuildArgs("/my/workspace", "/my/workspace/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, nil)

	if !containsSeq(args, "--bind", "/my/workspace", "/my/workspace") {
		t.Errorf("expected --bind /my/workspace /my/workspace in args: %v", args)
	}
}

func TestBuildArgs_SandboxHomeBind(t *testing.T) {
	sandboxHome := "/my/workspace/.steiner/home"
	args := BuildArgs("/my/workspace", sandboxHome, "/home/user",
		config.PermissionsConfig{}, nil, nil)

	if !containsSeq(args, "--bind", sandboxHome, sandboxHome) {
		t.Errorf("expected --bind %s %s in args: %v", sandboxHome, sandboxHome, args)
	}
}

func TestBuildArgs_TmpfsTmp(t *testing.T) {
	args := BuildArgs("/my/workspace", "/my/workspace/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, nil)

	if !slices.Contains(args, "--tmpfs") {
		t.Errorf("expected --tmpfs flag in args: %v", args)
	}

	idx := slices.Index(args, "--tmpfs")
	if idx < 0 || idx+1 >= len(args) || args[idx+1] != "/tmp" {
		t.Errorf("expected --tmpfs /tmp in args: %v", args)
	}
}

func TestBuildArgs_UnshareAll(t *testing.T) {
	args := BuildArgs("/ws", "/ws/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, nil)

	if !slices.Contains(args, "--unshare-all") {
		t.Errorf("expected --unshare-all in args: %v", args)
	}
	if !slices.Contains(args, "--share-net") {
		t.Errorf("expected --share-net in args: %v", args)
	}
}

func TestBuildArgs_NoSetenvHome(t *testing.T) {
	args := BuildArgs("/ws", "/ws/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, nil)

	if containsSeq(args, "--setenv", "HOME") {
		t.Errorf("--setenv HOME should not be present in args: %v", args)
	}
}

func TestBuildArgs_RoBindRoot(t *testing.T) {
	args := BuildArgs("/ws", "/ws/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, nil)

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
	args := BuildArgs("/my/workspace", "/my/workspace/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, nil)

	if !containsSeq(args, "--chdir", "/my/workspace") {
		t.Errorf("expected --chdir /my/workspace in args: %v", args)
	}
}

func TestBuildArgs_HostMountsAppended(t *testing.T) {
	hostMounts := []config.HostMount{
		{Path: "/data/ro", Mode: "ro"},
		{Path: "/data/rw", Mode: "rw"},
	}
	args := BuildArgs("/ws", "/ws/.steiner/home", "/home/user",
		config.PermissionsConfig{}, hostMounts, nil)

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

	args := BuildArgs("/ws", "/ws/.steiner/home", home,
		config.PermissionsConfig{}, nil, nil)

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

	args := BuildArgs("/ws", "/ws/.steiner/home", home,
		config.PermissionsConfig{}, nil, nil)

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

	args := BuildArgs("/ws", "/ws/.steiner/home", home,
		config.PermissionsConfig{}, nil, nil)

	if containsSeq(args, "--bind", cacheDir, cacheDir) {
		t.Errorf("should not mount missing cache dir: %v", args)
	}
}

func TestBuildArgs_NoCacheDirWhenNoHome(t *testing.T) {
	args := BuildArgs("/ws", "/ws/.steiner/home", "",
		config.PermissionsConfig{}, nil, nil)

	if containsSeq(args, "--bind", "/.cache", "/.cache") {
		t.Errorf("should not mount /.cache when userHome is empty: %v", args)
	}
}

func TestBuildArgs_AppendsOverlayBeforeChdir(t *testing.T) {
	overlayArgs := []string{
		"--tmpfs", "/etc/ssh/ssh_config.d",
		"--ro-bind-data", "3", "/etc/ssh/ssh_config",
	}

	args := BuildArgs("/ws", "/ws/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil, overlayArgs)

	if !containsSeq(args, overlayArgs...) {
		t.Fatalf("expected overlay args to be present: %v", args)
	}

	overlayIdx := slices.Index(args, "--ro-bind-data")
	chdirIdx := slices.Index(args, "--chdir")
	if overlayIdx < 0 || chdirIdx < 0 || overlayIdx > chdirIdx {
		t.Fatalf("expected overlay mounts before --chdir, args=%v", args)
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
