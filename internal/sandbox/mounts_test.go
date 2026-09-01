package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestBuildArgs_WorkspaceBind(t *testing.T) {
	args := BuildArgs("/my/workspace", "/my/workspace", "/my/workspace/.steiner/home", "/home/user", nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

	if !containsSeq(args, "--bind", "/my/workspace", "/my/workspace") {
		t.Errorf("expected --bind /my/workspace /my/workspace in args: %v", args)
	}
}

func TestBuildArgs_SeparateWritableRootAndWorkDir(t *testing.T) {
	args := BuildArgs("/repo", "/repo/.git-worktrees/step-1a", "/repo/.steiner/home", "/home/user", nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

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
	args := BuildArgs("/my/workspace", "/my/workspace", sandboxHome, "/home/user", nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

	if !containsSeq(args, "--bind", sandboxHome, sandboxHome) {
		t.Errorf("expected --bind %s %s in args: %v", sandboxHome, sandboxHome, args)
	}
}

func TestBuildArgs_TmpBind(t *testing.T) {
	args := BuildArgs("/my/workspace", "/my/workspace", "/my/workspace/.steiner/home", "/home/user", nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

	if !containsSeq(args, "--bind", "/tmp/sandbox-tmp", "/tmp") {
		t.Errorf("expected --bind /tmp/sandbox-tmp /tmp in args: %v", args)
	}
}

func TestBuildArgs_UnshareAll(t *testing.T) {
	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "/home/user", nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

	if !slices.Contains(args, "--unshare-all") {
		t.Errorf("expected --unshare-all in args: %v", args)
	}
	if !slices.Contains(args, "--share-net") {
		t.Errorf("expected --share-net in args: %v", args)
	}
}

func TestBuildArgs_NoSetenvHome(t *testing.T) {
	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "/home/user", nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

	if containsSeq(args, "--setenv", "HOME") {
		t.Errorf("--setenv HOME should not be present in args: %v", args)
	}
}

func TestBuildArgs_RoBindRoot(t *testing.T) {
	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "/home/user", nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

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
	args := BuildArgs("/my/workspace", "/my/workspace", "/my/workspace/.steiner/home", "/home/user", nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

	if !containsSeq(args, "--chdir", "/my/workspace") {
		t.Errorf("expected --chdir /my/workspace in args: %v", args)
	}
}

func TestBuildArgs_HostMountsAppended(t *testing.T) {
	hostMounts := []config.HostMount{
		{Path: "/data/ro", Mode: "ro"},
		{Path: "/data/rw", Mode: "rw"},
	}
	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "/home/user", hostMounts, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

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

	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", home, nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

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

	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", home, nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

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

	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", home, nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

	if containsSeq(args, "--bind", cacheDir, cacheDir) {
		t.Errorf("should not mount missing cache dir: %v", args)
	}
}

func TestBuildArgs_NoCacheDirWhenNoHome(t *testing.T) {
	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "", nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

	if containsSeq(args, "--bind", "/.cache", "/.cache") {
		t.Errorf("should not mount /.cache when userHome is empty: %v", args)
	}
}

func TestBuildArgs_AppendsOverlayBeforeChdir(t *testing.T) {
	overlayArgs := []string{
		"--tmpfs", "/etc/ssh/ssh_config.d",
		"--ro-bind-data", "3", "/etc/ssh/ssh_config",
	}

	args := BuildArgs("/ws", "/ws", "/ws/.steiner/home", "/home/user", nil, overlayArgs, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

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
	if err := os.MkdirAll(filepath.Join(root, ".steiner", "plans"), 0o755); err != nil {
		t.Fatalf("mkdir .steiner/plans: %v", err)
	}

	args := BuildArgs(root, root, filepath.Join(root, ".steiner", "home"), "/home/user", nil, nil, "/tmp/sandbox-tmp", true, config.PermissionsConfig{})

	steinerPlansPath := filepath.Join(root, ".steiner", "plans")
	if !containsSeq(args, "--ro-bind", root, root, "--bind", steinerPlansPath, steinerPlansPath) {
		t.Errorf("expected --ro-bind %s %s followed by --bind %s %s in args: %v", root, root, steinerPlansPath, steinerPlansPath, args)
	}

	if containsSeq(args, "--bind", root, root) {
		t.Errorf("expected --bind %s %s to be absent when readOnlyProject=true: %v", root, root, args)
	}
}

func TestBuildArgs_ReadOnlyProject_False_UnchangedFromBefore(t *testing.T) {
	root := "/test/root"
	args := BuildArgs(root, root, filepath.Join(root, ".steiner", "home"), "/home/user", nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

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

func TestBuildArgs_ReadOnlyProject_BindsGitDirWhenPresent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".steiner", "plans"), 0o755); err != nil {
		t.Fatalf("mkdir .steiner/plans: %v", err)
	}
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	args := BuildArgs(root, root, filepath.Join(root, ".steiner", "home"), "/home/user", nil, nil, "/tmp/sandbox-tmp", true, config.PermissionsConfig{})

	if !containsSeq(args, "--bind", gitDir, gitDir) {
		t.Errorf("expected --bind %s %s in args: %v", gitDir, gitDir, args)
	}

	// Must land after the root ro-bind so it wins (later bwrap ops take priority).
	roIdx := indexOfSeq(args, "--ro-bind", root, root)
	gitIdx := indexOfSeq(args, "--bind", gitDir, gitDir)
	if roIdx < 0 || gitIdx < 0 || gitIdx < roIdx {
		t.Errorf("expected .git bind after root ro-bind: args=%v", args)
	}
}

func TestBuildArgs_ReadOnlyProject_NoGitBindWhenAbsent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".steiner", "plans"), 0o755); err != nil {
		t.Fatalf("mkdir .steiner/plans: %v", err)
	}

	args := BuildArgs(root, root, filepath.Join(root, ".steiner", "home"), "/home/user", nil, nil, "/tmp/sandbox-tmp", true, config.PermissionsConfig{})

	gitDir := filepath.Join(root, ".git")
	if containsSeq(args, "--bind", gitDir, gitDir) {
		t.Errorf("expected no .git bind when .git is absent: %v", args)
	}
}

func TestBuildArgs_ReadOnlyProject_False_NoGitBind(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	args := BuildArgs(root, root, filepath.Join(root, ".steiner", "home"), "/home/user", nil, nil, "/tmp/sandbox-tmp", false, config.PermissionsConfig{})

	gitDir := filepath.Join(root, ".git")
	if containsSeq(args, "--bind", gitDir, gitDir) {
		t.Errorf("expected no explicit .git bind when readOnlyProject=false (whole root already writable): %v", args)
	}
}

func TestGitWritableBinds_PlainRepo(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	binds := gitWritableBinds(root)
	if !slices.Contains(binds, gitDir) || len(binds) != 1 {
		t.Errorf("expected binds=[%s], got %v", gitDir, binds)
	}
}

func TestGitWritableBinds_Missing(t *testing.T) {
	root := t.TempDir()

	if binds := gitWritableBinds(root); binds != nil {
		t.Errorf("expected nil binds when .git is absent, got %v", binds)
	}
}

func TestGitWritableBinds_LinkedWorktree(t *testing.T) {
	root := t.TempDir()
	mainRepo := t.TempDir()
	commonDir := filepath.Join(mainRepo, ".git")
	worktreeGitDir := filepath.Join(commonDir, "worktrees", "wt")
	if err := os.MkdirAll(worktreeGitDir, 0o755); err != nil {
		t.Fatalf("mkdir worktree gitdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreeGitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatalf("write commondir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+worktreeGitDir+"\n"), 0o644); err != nil {
		t.Fatalf("write .git pointer: %v", err)
	}

	binds := gitWritableBinds(root)
	wantGitFile := filepath.Join(root, ".git")
	if !slices.Contains(binds, wantGitFile) {
		t.Errorf("expected binds to include .git pointer file %s: %v", wantGitFile, binds)
	}
	if !slices.Contains(binds, worktreeGitDir) {
		t.Errorf("expected binds to include worktree gitdir %s: %v", worktreeGitDir, binds)
	}
	if !slices.Contains(binds, commonDir) {
		t.Errorf("expected binds to include common dir %s: %v", commonDir, binds)
	}
}

func TestGitWritableBinds_FileWithoutGitdirPrefix(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("not a gitdir pointer\n"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}

	if binds := gitWritableBinds(root); binds != nil {
		t.Errorf("expected nil binds for malformed .git file, got %v", binds)
	}
}

// indexOfSeq returns the index of the first occurrence of needle as a
// contiguous subsequence in haystack, or -1 if not found.
func indexOfSeq(haystack []string, needle ...string) int {
	if len(needle) == 0 {
		return -1
	}
outer:
	for i := 0; i+len(needle) <= len(haystack); i++ {
		for j, n := range needle {
			if haystack[i+j] != n {
				continue outer
			}
		}
		return i
	}
	return -1
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

func TestWritableHostMounts(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.SandboxConfig
		want []string
	}{
		{
			name: "no mounts",
			cfg:  config.SandboxConfig{},
			want: nil,
		},
		{
			name: "empty mode is read-only",
			cfg: config.SandboxConfig{HostMounts: []config.HostMount{
				{Path: "/host/bare", Mode: ""},
			}},
			want: nil,
		},
		{
			name: "ro mount excluded",
			cfg: config.SandboxConfig{HostMounts: []config.HostMount{
				{Path: "/host/ro", Mode: "ro"},
			}},
			want: nil,
		},
		{
			name: "rw only, preserving order",
			cfg: config.SandboxConfig{HostMounts: []config.HostMount{
				{Path: "/host/ro", Mode: "ro"},
				{Path: "/host/rw1", Mode: "rw"},
				{Path: "/host/bare", Mode: ""},
				{Path: "/host/rw2", Mode: "rw"},
			}},
			want: []string{"/host/rw1", "/host/rw2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WritableHostMounts(tt.cfg)
			if !slices.Equal(got, tt.want) {
				t.Errorf("WritableHostMounts() = %v, want %v", got, tt.want)
			}
		})
	}
}
