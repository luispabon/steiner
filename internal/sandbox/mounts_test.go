package sandbox

import (
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestBuildArgs_WorkspaceBind(t *testing.T) {
	args := BuildArgs("/my/workspace", "/my/workspace/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil)

	if !containsSeq(args, "--bind", "/my/workspace", "/workspace") {
		t.Errorf("expected --bind /my/workspace /workspace in args: %v", args)
	}
}

func TestBuildArgs_SandboxHomeBind(t *testing.T) {
	sandboxHome := "/my/workspace/.steiner/home"
	args := BuildArgs("/my/workspace", sandboxHome, "/home/user",
		config.PermissionsConfig{}, nil)

	if !containsSeq(args, "--bind", sandboxHome, "/home/steiner") {
		t.Errorf("expected --bind %s /home/steiner in args: %v", sandboxHome, args)
	}
}

func TestBuildArgs_TmpfsTmp(t *testing.T) {
	args := BuildArgs("/my/workspace", "/my/workspace/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil)

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
		config.PermissionsConfig{}, nil)

	if !slices.Contains(args, "--unshare-all") {
		t.Errorf("expected --unshare-all in args: %v", args)
	}
	if !slices.Contains(args, "--share-net") {
		t.Errorf("expected --share-net in args: %v", args)
	}
}

func TestBuildArgs_SetenvHome(t *testing.T) {
	args := BuildArgs("/ws", "/ws/.steiner/home", "/home/user",
		config.PermissionsConfig{}, nil)

	if !containsSeq(args, "--setenv", "HOME", "/home/steiner") {
		t.Errorf("expected --setenv HOME /home/steiner in args: %v", args)
	}
}

func TestBuildArgs_HostMountsAppended(t *testing.T) {
	hostMounts := []config.HostMount{
		{Path: "/data/ro", Mode: "ro"},
		{Path: "/data/rw", Mode: "rw"},
	}
	args := BuildArgs("/ws", "/ws/.steiner/home", "/home/user",
		config.PermissionsConfig{}, hostMounts)

	if !containsSeq(args, "--ro-bind", "/data/ro", "/data/ro") {
		t.Errorf("expected --ro-bind /data/ro /data/ro in args: %v", args)
	}
	if !containsSeq(args, "--bind", "/data/rw", "/data/rw") {
		t.Errorf("expected --bind /data/rw /data/rw in args: %v", args)
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
