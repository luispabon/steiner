package skills_test

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/skill"
	"github.com/luispabon/steiner/skills"
)

// The blocks below are deliberately duplicated verbatim across bundled
// skills. This test fails if any copy drifts from the others. Each block
// declares which skills carry it — not every block is in all three.
const worktreeHandlingBlock = "### Worktree Handling\n" +
	"\n" +
	"Every `code` sub-agent runs in its own runtime-provisioned and runtime-verified git worktree on a `delegate/` branch under `.steiner/worktrees/`; you arrange nothing yourself.\n" +
	"\n" +
	"1. Read `worktree_path` and `worktree_branch` from the delegation result.\n" +
	"2. Check `warnings` for entries noting uncommitted parent-tree changes the child could not see — every worktree branches from the parent's HEAD, so commit those on the feature branch before the next dispatch if the child needs them.\n" +
	"3. `follow_up` results do not repopulate `worktree_path`/`worktree_branch`; retain the values from the initial `code` result across any follow-up calls on the same agent.\n" +
	"4. After reviewing a step's result, merge the returned branch into the feature branch first, then remove the worktree and delete the branch, in that order: `git worktree remove <worktree-path>`, then `git branch -D <worktree-branch>`.\n"

const preCommitChecklistBlock = "### Pre-Commit Checklist\n" +
	"\n" +
	"Include this checklist verbatim in every delegated task that commits. The sub-agent must run all checks before `git commit`.\n" +
	"\n" +
	"1. `git branch --show-current` — must start with `delegate/`. If it shows the feature branch, STOP and report without committing.\n" +
	"2. `git status` — must show only files within the declared scope as modified. If unexpected files appear, STOP and report.\n"

// fixDelegationBulletsBlock is shared by the review and simplify fix loops
// only. The surrounding prose deliberately differs ("The review-fix delegated
// agent must:" vs "The fix delegated agent must:"), so the pinned span is the
// bullet list itself rather than the whole section.
const fixDelegationBulletsBlock = "- receive only the approved findings, fix plan, relevant files, constraints, and verification strategy\n" +
	"- run the pre-commit checklist before committing (see below)\n" +
	"- commit its changes on the runtime-provided `delegate/` branch\n" +
	"- avoid unrelated cleanup or scope expansion\n" +
	"- not merge, rebase, or clean up reviewer-owned git state\n"

func TestSharedBlocksAreByteIdenticalAcrossSkills(t *testing.T) {
	skillNames := []string{"implement", "review", "simplify"}

	blocks := []struct {
		name   string
		text   string
		skills []string
	}{
		{"worktree handling", worktreeHandlingBlock, skillNames},
		{"pre-commit checklist", preCommitChecklistBlock, skillNames},
		{"fix delegation bullets", fixDelegationBulletsBlock, []string{"review", "simplify"}},
	}

	loader := skill.Loader{BundledFS: skills.FS}
	contents := make(map[string]string, len(skillNames))
	for _, name := range skillNames {
		loaded, err := loader.Load(context.Background(), name)
		if err != nil {
			t.Fatalf("load skill %q: %v", name, err)
		}
		contents[name] = loaded.Content
	}

	for _, b := range blocks {
		t.Run(b.name, func(t *testing.T) {
			for _, name := range b.skills {
				count := strings.Count(contents[name], b.text)
				if count != 1 {
					t.Errorf("skills/%s/SKILL.md contains the %q block %d times, want exactly 1", name, b.name, count)
				}
			}
		})
	}
}
