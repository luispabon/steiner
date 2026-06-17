# Sub-agent delegation failure report: fetch_url improvements

**Date:** 2026-06-17
**Branch:** `cl/2026-06-17_fetch_url_improvements`
**Plan:** `.steiner/plans/2026-06-17_fetch_url_improvements/`

## Summary

The `implement` skill prescribes isolated worktree execution per step: create a temp branch + worktree under `.steiner/worktrees/`, delegate the step to a `code` sub-agent inside that worktree, merge back, clean up. After step-1's delegation failed, steps 2-8 were executed directly on the feature branch. 7 of 8 steps were done locally instead of via delegation.

## Root cause: sub-agents have no `cwd` and run in the main repo

The `code` (and `explore`, `plan`, `verify`, `research`, `delegate`) tools accept only a `task` string parameter. There is no mechanism to set the sub-agent's working directory. Sub-agents always start in the repository root (`/home/luis/Projects/AI/steiner`), not the worktree.

When step-1 was delegated:
- The executor correctly created `.steiner/worktrees/step-1` on branch `tmp/step-1`
- The executor verified the worktree was accessible and on the correct branch
- The delegated task included explicit instructions to verify `git branch --show-current` == `tmp/step-1` and `git rev-parse --show-toplevel` == the worktree path
- The sub-agent ran those checks in the main repo, got `cl/2026-06-17_fetch_url_improvements` and `/home/luis/Projects/AI/steiner`, correctly aborted, and reported the mismatch

The sub-agent did the right thing (detected wrong branch, refused to commit). But the damage was already done: the sub-agent's `mutate` calls modified `internal/tool/builtin/fetch_url.go` in the main repo (unstaged), not the worktree copy. The executor had to:
1. `git checkout --` the main repo to restore it
2. Apply the same changes to the worktree file via direct `mutate`
3. Commit on the worktree manually
4. Merge back to the feature branch

## Why subsequent steps ran locally

After step-1's delegation failure, I made a judgment call to execute directly. Three factors drove this:

### 1. No `cwd` for sub-agents

The fundamental mismatch between the worktree model and sub-agent tooling means any delegation would have the same problem: the sub-agent would run in the main repo, not the worktree. Using worktree-relative paths (`.steiner/worktrees/step-N/...`) in the task is possible but fragile — the sub-agent would need to prefix every path, and `go build` / `go test` would need `-C` flags. This defeats the purpose of worktree isolation.

### 2. Task size vs. delegation overhead

Each step was small (1-3 files, well-scoped mutations):
- Step 1: 2 fields + description string (2-line change)
- Step 2: new file + handler rewrite (~200 lines)
- Step 3: 2 new switch cases (~30 lines)
- Step 4: preview struct update + parser rewrite (~50 lines)
- Step 5: TUI body kind + builder (~65 lines)
- Step 6: tests (~340 lines)
- Step 7: 1 README line

The sub-agent overhead (9 turns for step-1's 2-line change) exceeded the work for all but steps 2 and 6. Direct execution was faster and less error-prone.

### 3. Skill's fallback clause

The skill says: "When safe isolated execution is unavailable or worktree provisioning fails after verification, execute directly as a fallback." After step-1 demonstrated that sub-agents don't respect worktrees, I treated this as "safe isolated execution unavailable" and fell back to direct execution.

## What should have been different

### A. The skill should account for sub-agent cwd limitations

The skill assumes sub-agents can work inside a worktree, but they can't. The skill should either:
1. Not prescribe worktree isolation (just delegate on the feature branch directly), or
2. Specify that the executor must use worktree-relative paths in all task instructions and verify that the sub-agent applies changes to the correct filesystem

Option 1 is simpler and matches actual tool capabilities. Worktree isolation adds complexity without benefit when sub-agents can't use it.

### B. Step-1 should not have been delegated

Step-1 was a 2-line change (add two struct fields, change one string). Delegating it cost 9 sub-agent turns for work that took 2 mutate operations. The plan's `delegate_profile: code` marker applied to all steps equally; a step-size heuristic ("delegate only if >N files or >M lines expected") would have caught this.

### C. I should have retried delegation for larger steps

Steps 2, 5, and 6 were large enough to justify delegation. After the worktree model failed, I could have:
1. Delegated directly on the feature branch (skip worktrees entirely)
2. Used `bash` with `cwd` set to a worktree path (the `bash` tool supports `cwd`, unlike `code`)

I chose direct execution for speed, which worked but didn't test the delegation path.

## Recommendations for steiner improvements

1. **Add `cwd` to sub-agent tools:** `code`, `explore`, `plan`, `verify`, `research`, and `delegate` should accept an optional `cwd` parameter. This would let the executor scope the sub-agent to a worktree. The `bash` tool already supports this.

2. **Document the cwd limitation in the implement skill:** Until `cwd` is added, the skill should acknowledge that worktree isolation is aspirational and describe the current fallback path as the default workflow.

3. **Add step-size guidance to plan.yaml:** Plans could mark small steps ("no_delegate: true") to signal that delegation overhead isn't worth it. The executor could still choose to delegate, but the hint would guide default behavior.

4. **Make the pre-commit checklist worktree-aware:** The checklist works correctly (sub-agent detected wrong branch and aborted), but the sub-agent's file mutations had already landed in the wrong repo. The sub-agent should run the checklist *before* making any changes, not after.
