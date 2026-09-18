package oneshot

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSlugFromTask(t *testing.T) {
	tests := []struct {
		name string
		task string
		want string
	}{
		{name: "basic", task: "Build the parser", want: "build-the-parser"},
		{name: "punctuation", task: "Fix: the /oneshot flow!", want: "fix-the-oneshot-flow"},
		{name: "empty", task: "   ", want: "run"},
		{
			// 49 bytes (one ASCII byte plus 16 three-byte runes) forces the byte
			// truncation to land mid-rune, so the cut must back off to byte 46.
			name: "multibyte truncation",
			task: "a" + strings.Repeat("漢", 16),
			want: "a" + strings.Repeat("漢", 15),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SlugFromTask(tt.task)
			if !utf8.ValidString(got) {
				t.Fatalf("SlugFromTask(%q) = %q, want valid UTF-8", tt.task, got)
			}
			if got != tt.want {
				t.Fatalf("SlugFromTask(%q) = %q, want %q", tt.task, got, tt.want)
			}
		})
	}
}

func TestRunIdentityPaths(t *testing.T) {
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	root := "/project"
	worktree := "/project/.steiner/worktrees/oneshot-abc123"

	if got, want := identity.BranchName(), "oneshot/build-parser-abc123"; got != want {
		t.Fatalf("BranchName() = %q, want %q", got, want)
	}
	if got, want := identity.WorktreePath(root), worktree; got != want {
		t.Fatalf("WorktreePath() = %q, want %q", got, want)
	}
	if got, want := identity.PlanningPath(worktree), "/project/.steiner/worktrees/oneshot-abc123/.steiner/plans/oneshot-abc123"; got != want {
		t.Fatalf("PlanningPath() = %q, want %q", got, want)
	}
	if got, want := identity.StateDir(root), "/project/.steiner/oneshot/abc123"; got != want {
		t.Fatalf("StateDir() = %q, want %q", got, want)
	}
	if got, want := identity.ManifestPath(root), "/project/.steiner/oneshot/abc123/run.json"; got != want {
		t.Fatalf("ManifestPath() = %q, want %q", got, want)
	}
	if got, want := identity.LockPath(root), "/project/.steiner/oneshot/abc123/run.lock"; got != want {
		t.Fatalf("LockPath() = %q, want %q", got, want)
	}
}
