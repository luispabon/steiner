package prompt

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/luispabon/steiner/internal/skill"
)

// makeLoaderBundledFS builds a test fs.FS for skill.Loader tests.
func makeLoaderBundledFS(t *testing.T, name, content string) fs.FS {
	t.Helper()
	return fstest.MapFS{
		name + "/SKILL.md": &fstest.MapFile{Data: []byte(content)},
	}
}

func TestLoadSkillBlocksEmptyNames(t *testing.T) {
	t.Parallel()

	blocks, err := loadSkillBlocks(context.Background(), skill.Loader{}, nil)
	if err != nil {
		t.Fatalf("loadSkillBlocks() error = %v", err)
	}
	if blocks != nil {
		t.Fatalf("loadSkillBlocks() = %v, want nil", blocks)
	}
}

func TestLoadSkillBlocksFromFilesystem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "codex"), "SKILL.md", "filesystem skill")

	loader := skill.Loader{RootDirs: []string{root}}
	blocks, err := loadSkillBlocks(context.Background(), loader, []string{"codex"})
	if err != nil {
		t.Fatalf("loadSkillBlocks() error = %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if got, want := blocks[0].Content, "filesystem skill"; got != want {
		t.Fatalf("block content = %q, want %q", got, want)
	}
	if got, want := blocks[0].Source, ContextSourceSkill; got != want {
		t.Fatalf("block source = %q, want %q", got, want)
	}
}

func TestLoadSkillBlocksFromBundledFS(t *testing.T) {
	t.Parallel()

	bfs := makeLoaderBundledFS(t, "plan", "bundled plan instructions")
	loader := skill.Loader{BundledFS: bfs}

	blocks, err := loadSkillBlocks(context.Background(), loader, []string{"plan"})
	if err != nil {
		t.Fatalf("loadSkillBlocks() error = %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if got, want := blocks[0].Content, "bundled plan instructions"; got != want {
		t.Fatalf("block content = %q, want %q", got, want)
	}
	if got, want := blocks[0].Source, ContextSourceSkill; got != want {
		t.Fatalf("block source = %q, want %q", got, want)
	}
}

func TestLoadSkillBlocksBundledPrecedence(t *testing.T) {
	t.Parallel()

	// Filesystem has a same-named skill.
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "codex"), "SKILL.md", "filesystem version")

	// Bundled FS also has it.
	bfs := makeLoaderBundledFS(t, "codex", "bundled version")

	loader := skill.Loader{RootDirs: []string{root}, BundledFS: bfs}
	blocks, err := loadSkillBlocks(context.Background(), loader, []string{"codex"})
	if err != nil {
		t.Fatalf("loadSkillBlocks() error = %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	// Bundled must win.
	if got, want := blocks[0].Content, "bundled version"; got != want {
		t.Fatalf("block content = %q, want %q (bundled should shadow filesystem)", got, want)
	}
}

func TestLoadSkillBlocksMissingName(t *testing.T) {
	t.Parallel()

	loader := skill.Loader{}
	_, err := loadSkillBlocks(context.Background(), loader, []string{"nonexistent"})
	if err == nil {
		t.Fatalf("loadSkillBlocks() expected error for missing skill")
	}
}

func TestSkillFramingBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		names []string
		want  string
	}{
		{
			name:  "multiple skills",
			names: []string{"plan", "review"},
			want:  "Enabled: plan, review",
		},
		{
			name:  "single skill",
			names: []string{"plan"},
			want:  "Enabled: plan",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			block := skillFramingBlock(tc.names)
			if got, want := block.Source, ContextSourceSkill; got != want {
				t.Fatalf("block source = %q, want %q", got, want)
			}
			if !strings.Contains(block.Content, "## Active Skills") {
				t.Fatalf("block content missing heading: %q", block.Content)
			}
			if !strings.Contains(block.Content, tc.want) {
				t.Fatalf("block content = %q, want substring %q", block.Content, tc.want)
			}
			if !strings.Contains(block.Content, "must follow when the user's request matches its domain") {
				t.Fatalf("block content missing workflow directive: %q", block.Content)
			}
			if len(tc.names) == 1 && strings.Contains(block.Content, "plan,") {
				t.Fatalf("single skill content has trailing comma: %q", block.Content)
			}
		})
	}
}
