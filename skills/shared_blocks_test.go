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
const worktreeProvisioningBlock = "### Worktree Provisioning\n" +
	"\n" +
	"Always create worktrees under `.steiner/worktrees/` inside the project root. Do not use `/tmp` or other system temporary directories — they may be sandboxed and silently fail.\n" +
	"\n" +
	"After running `git worktree add`, verify the directory actually exists:\n" +
	"\n" +
	"1. Run `ls -d <worktree-path>` to confirm the directory was created.\n" +
	"2. Run `git -C <worktree-path> branch --show-current` to confirm it is on the expected temporary branch.\n" +
	"3. If either check fails, prune the worktree entry with `git worktree remove <worktree-path>` and fall back to direct delegation.\n"

const preCommitChecklistBlock = "### Pre-Commit Checklist\n" +
	"\n" +
	"Include the appropriate checklist verbatim in every delegated task that commits. The sub-agent must run all checks before `git commit`.\n" +
	"\n" +
	"**Isolated delegation mode:**\n" +
	"\n" +
	"1. `git branch --show-current` — must equal the temporary branch name given in the task. If it shows the feature branch, STOP and report without committing.\n" +
	"2. `git rev-parse --show-toplevel` — must equal the worktree path given in the task. If it shows a different path, STOP and report without committing.\n" +
	"3. `git status` — must show only files within the declared scope as modified. If unexpected files appear, STOP and report.\n" +
	"\n" +
	"**Direct delegation mode:**\n" +
	"\n" +
	"1. `git branch --show-current` — must equal the feature branch name given in the task. If it shows a different branch, STOP and report without committing.\n" +
	"2. `git status` — must show only files within the declared scope as modified. If unexpected files appear, STOP and report.\n"

// fixDelegationBulletsBlock is shared by the review and simplify fix loops
// only. The surrounding prose deliberately differs ("The review-fix delegated
// agent must:" vs "The fix delegated agent must:"), so the pinned span is the
// bullet list itself rather than the whole section.
const fixDelegationBulletsBlock = "- receive only the approved findings, fix plan, relevant files, constraints, and verification strategy\n" +
	"- run the pre-commit checklist before committing (see below)\n" +
	"- commit its changes on the working branch (temporary branch for isolated delegation, feature branch for direct delegation)\n" +
	"- avoid unrelated cleanup or scope expansion\n" +
	"- not merge, rebase, or clean up reviewer-owned git state\n"

func TestSharedBlocksAreByteIdenticalAcrossSkills(t *testing.T) {
	skillNames := []string{"implement", "review", "simplify"}

	blocks := []struct {
		name   string
		text   string
		skills []string
	}{
		{"worktree provisioning", worktreeProvisioningBlock, skillNames},
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
