---
name: review
description: Review coding changes against the approved plan, identify concrete bugs, regressions, and plan-adherence gaps, run approved isolated fix passes, then close the loop with optional planning-doc cleanup and PR/MR creation. Use after execution is complete.
---

# Coding Loop Reviewer And Closeout

## Overview

Use this skill after execution. Validate the implemented work against `overview.md`, `plan.yaml`, `execution.md`, and the actual repository state. If needed, run a bounded review-fix loop. When the review passes, own closeout: optional planning-doc cleanup and optional PR/MR creation.

The reviewer is the final gate for the loop.

## Input Contract

- Require one argument: the feature planning folder.
- Require `overview.md`, `plan.yaml`, and `execution.md`.
- Stop if artifacts are missing, conflict materially, or show execution did not reach reviewer handoff.
- If execution reached reviewer handoff only by explicit user request despite known blockers, review the blocked implementation only within that stated purpose.
- Derive the expected branch as `cl/YYYY-MM-DD_FEATURE_NAME`.
- Require the expected branch to exist and be clean before review starts, except for safe reviewer artifact initialization.
- Check whether `.steiner/plans/` is gitignored by running `git check-ignore -q .steiner/plans/`. If exit code is 0, planning artifacts are local-only — do not stage or commit them at any point during this workflow. If exit code is non-zero, planning artifacts are version-controlled — commit them as described below.
- Treat planner and executor artifacts as immutable inputs unless the user explicitly requests replanning or artifact correction.

## Review Flow

Follow this sequence:

1. Validate artifacts and branch state.
2. Check out the expected feature branch.
3. Run the review pass.
4. If blocking findings exist, ask approval for one consolidated fix plan.
5. Run approved review-fix work through Steiner delegation.
6. Rerun relevant checks.
7. Repeat only if new blocking findings remain.
8. Mark final status. The advisor sanity check (### Advisor Sanity Check) must complete before marking.
9. Offer closeout actions: planning-doc cleanup and PR/MR creation.

Stop and report blockers instead of widening scope.

## Review Artifact

`review.md` is optional.

Do not create `review.md` by default. Create it only when the user chooses to keep planning docs or declines planning-doc deletion during closeout.

When created, `review.md` is a compact final-state file under the planning folder. It should record:

- scope and inputs reviewed
- review status: `fail`, `pass_with_notes`, or `pass`
- blocking findings and resolution state
- non-blocking notes
- verification reruns and results
- residual risks
- closeout actions chosen and results

Do not maintain a verbose review history unless needed to preserve finding resolution.

## Review Standard

Compare these inputs:

- `overview.md` for original intent, boundaries, and verification strategy
- `plan.yaml` for the approved implementation contract
- `execution.md` for completed steps, deviations, and verification
- repository state for what actually landed

Focus on:

- plan and scope adherence — including whether each step's implementation honors its `approach` and the Key Decisions cited in its `decisions` list
- obvious bugs or regressions
- missing or weak verification
- correctness against accepted intent
- maintainability issues that materially affect correctness or future work

Review touched files and directly adjacent regression-risk areas: call sites, interfaces, tests, config, data paths, and package boundaries touched by or directly depending on the change. Do not broadly re-review unrelated code.

For inspection, use native tools directly — `read` to examine files, `grep` and `glob` to locate code and call sites, `mutate` for `review.md`. Do not route through `bash` when a dedicated tool exists.

Prefer evidence over speculation. Findings should reference concrete code, artifacts, missing checks, or reproducible reasoning.

## Findings

Classify findings as:

- `blocking`: must be fixed before closeout
- `non_blocking`: record but does not block closeout
- `informational`: useful note only

Map final status as:

- `fail` if blocking findings remain
- `pass_with_notes` if no blocking findings remain but non-blocking findings remain
- `pass` if only informational findings or no findings remain

Only fixable blocking findings enter the fix plan by default. Include non-blocking fixes only when directly adjacent and negligible in scope.

## Review-Fix Loop

Do not edit code before a review pass has produced concrete findings and the user has approved the fix plan.

Each review pass may produce one consolidated fix plan. The plan must map fixes to blocking finding ids and state which verification will be rerun.

The reviewer prefers the highest available delegation tier for fix passes:

1. **Isolated delegation** (preferred): fix pass runs in a dedicated worktree on a temporary branch. Provides full isolation from the feature branch.
2. **Direct delegation** (fallback): fix pass runs directly on the feature branch via a `code` sub-agent. Used when worktrees are unavailable or provisioning fails.
3. **Inline fixes** (last resort): reviewer applies fixes directly. Used only when delegation tools themselves are unavailable.

Prefer isolated delegation. Fall back through tiers in order. Do not skip direct delegation and jump to inline fixes just because worktrees failed. A judgment that isolation is unnecessary or that the fixes are simple does not justify skipping isolated delegation — only concrete errors (worktree provisioning failure, sub-agent dispatch error) do.

The reviewer owns provisioning, merge, verification, cleanup, and any `review.md` updates.

### Worktree Provisioning

Always create worktrees under `.steiner/worktrees/` inside the project root. Do not use `/tmp` or other system temporary directories — they may be sandboxed and silently fail.

After running `git worktree add`, verify the directory actually exists:

1. Run `ls -d <worktree-path>` to confirm the directory was created.
2. Run `git -C <worktree-path> branch --show-current` to confirm it is on the expected temporary branch.
3. If either check fails, prune the worktree entry with `git worktree remove <worktree-path>` and fall back to direct delegation.

### Review-Fix Delegation

The review-fix delegated agent must:

- receive only the approved findings, fix plan, relevant files, constraints, and verification strategy
- run the pre-commit checklist before committing (see below)
- commit its changes on the working branch (temporary branch for isolated delegation, feature branch for direct delegation)
- avoid unrelated cleanup or scope expansion
- not merge, rebase, or clean up reviewer-owned git state

Review-fix work is sequential. Do not parallelize it.

### Pre-Commit Checklist

Include the appropriate checklist verbatim in every delegated task that commits. The sub-agent must run all checks before `git commit`.

**Isolated delegation mode:**

1. `git branch --show-current` — must equal the temporary branch name given in the task. If it shows the feature branch, STOP and report without committing.
2. `git rev-parse --show-toplevel` — must equal the worktree path given in the task. If it shows a different path, STOP and report without committing.
3. `git status` — must show only files within the declared scope as modified. If unexpected files appear, STOP and report.

**Direct delegation mode:**

1. `git branch --show-current` — must equal the feature branch name given in the task. If it shows a different branch, STOP and report without committing.
2. `git status` — must show only files within the declared scope as modified. If unexpected files appear, STOP and report.

If any check fails, the sub-agent must not commit. It must report the mismatch and let the reviewer recover.

### Advisor Sanity Check

Run the advisor tool between the review-fix loop and final verification, before marking final status. Pass `files` with the review artifacts (e.g. `review.md` or the plan's `overview.md`/`plan.yaml`) and a `question` stating the findings the reviewer is about to mark final, so the advisor judges the actual artifacts rather than a conversational summary. Unconditional — skip only if the per-run advisor budget is exhausted or `AdvisorEnabled` is off.

Include the advisor's note in the final review status summary. When `review.md` is created during closeout, append the note to the file.

## Verification After Fixes

Before running `make check` or `golangci-lint run`, run `golangci-lint cache clean` to avoid false positives from stale cache entries pointing at deleted worktree paths.

Reuse the verification strategy in `overview.md` by default. Rerun the narrowest checks that cover the fixes and any affected acceptance criteria.

If verification fails, either run another approved review-fix pass or report a blocker. Do not silently downgrade failures.

## Steiner Delegation

Use Steiner's specialised tools directly:

- `explore({"task": "..."})` for targeted review discovery
- `code({"task": "..."})` for approved review-fix passes
- `verify({"task": "..."})` for check-only verification
- `plan({"task": "..."})` for bounded analysis of a review uncertainty
- `research({"task": "..."})` only for approved current/external research
- `delegate({...})` only when no specialised profile fits

Specialised Steiner tools accept only `task`. The reviewer must provide a tight, self-contained task with known context, relevant files, approved decisions, constraints, expected output, non-goals, and the appropriate pre-commit checklist from the Review-Fix Loop section.

## Closeout

Closeout starts only when final review status is `pass` or `pass_with_notes`, blocking findings are resolved, and the feature branch is clean.

Offer the user these closeout actions:

- stop after review approval
- delete the planning folder
- prepare and create a PR/MR
- both delete planning folder and prepare/create a PR/MR

Do not delete planning artifacts, push, or create a PR/MR without explicit user confirmation.

## Planning-Doc Cleanup

Planning docs are disposable after successful review unless the user wants to keep them.

If the user chooses cleanup:

- delete `.steiner/plans/YYYY-MM-DD_FEATURE_NAME/`

- if planning artifacts are version-controlled, commit the deletion unless the user explicitly asks otherwise
- include the cleanup result in the closeout message

If the user declines cleanup, create or update `review.md` with the final review and closeout state.

## PR Or MR Preparation

Use commit messages for PR/MR summaries. Do not broad-diff the branch against `origin/main` by default.

Build the PR/MR body from:

- the `## Overview` section of `overview.md`
- non-merge commit messages created for the loop
- final review status and any residual risks from the current review

Use loop artifacts only as fallback if they still exist and commit messages are missing, vague, contradictory, or clearly insufficient.

Inspect targeted diffs only if commit messages and available artifacts are insufficient.

Detect the remote provider from the current branch tracking remote or `origin`.

Choose the target branch in this order:

1. upstream or tracking base if clearly known
2. `origin/main`
3. `origin/master`
4. ask the user

Use only known provider flows:

- GitHub: push if needed, then `gh pr create --title <title> --body <body> --base <target> --head <branch>`
- GitLab: `git push -o merge_request.create -o merge_request.target=<target> <remote> <branch>`
- Azure DevOps: push if needed, then `az repos pr create --title <title> --description <body> --source-branch <branch> --target-branch <target>`
- Bitbucket or unknown provider: report unsupported automatic PR/MR creation

Ask for confirmation before pushing or creating a PR/MR. Do not dump the full PR/MR body unless the user asks.

## Completion

The loop is complete when review has passed and the user has either accepted, declined, or completed closeout actions. Unsupported PR/MR creation does not invalidate the reviewed work; it only prevents automatic PR/MR creation.
