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

For inspection, use native tools directly — `read` to examine files, `grep` and `glob` to locate code and call sites, `mutate` for `review.md`.

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

The reviewer prefers delegated fixes over inline work:

1. **Delegated fix pass** (preferred): a `code` sub-agent runs the fix in its own runtime-provisioned, runtime-verified worktree on a `delegate/` branch. Provisioning failure fails the `code` call outright — that is a blocker to report, not a cue to fix inline.
2. **Inline fixes** (last resort): reviewer applies fixes directly. Used only when delegation tools themselves are unavailable. Deliberate exception to the routing threshold in your system prompt: review-fix work must have a guaranteed way to close out even when delegation tooling itself is down.

A judgment that the fixes are simple does not justify skipping delegation — only delegation tooling itself being unavailable does.

The reviewer owns merge, verification, cleanup, and any `review.md` updates.

### Warm Follow-Up Policy

Resume a suitable warm agent before cold dispatch only when it remains available for the same bounded deliverable in the same still-live workspace and scope. Follow-ups are sequential. Retain the responsible fix agent through related correction loops until it is closed or its worktree is merged and deleted, even if the session reports resumable. A resumable session alone does not prove that an isolated worktree still exists. Use the responsible implementation agent for related corrections and the original reviewer only for a narrow re-check. Use fresh delegation for unavailable or non-resumable sessions, material lane or scope changes, independent or wider review, or removed worktrees. Workflow handoffs are not safe continuation boundaries.

### Worktree Handling

Every `code` sub-agent runs in its own runtime-provisioned and runtime-verified git worktree on a `delegate/` branch under `.steiner/worktrees/`; you arrange nothing yourself.

1. Read `worktree_path` and `worktree_branch` from the delegation result.
2. Check `warnings` for entries noting uncommitted parent-tree changes the child could not see — every worktree branches from the parent's HEAD, so commit those on the feature branch before the next dispatch if the child needs them.
3. `follow_up` results do not repopulate `worktree_path`/`worktree_branch`; retain the values from the initial `code` result across any follow-up calls on the same agent.
4. After reviewing a step's result, merge the returned branch into the feature branch first, then remove the worktree and delete the branch, in that order: `git worktree remove <worktree-path>`, then `git branch -D <worktree-branch>`.

### Review-Fix Delegation

The review-fix delegated agent must:

- receive only the approved findings, fix plan, relevant files, constraints, and verification strategy
- run the pre-commit checklist before committing (see below)
- commit its changes on the runtime-provided `delegate/` branch
- avoid unrelated cleanup or scope expansion
- not merge, rebase, or clean up reviewer-owned git state

Review-fix work is sequential. Do not parallelize it.

### Pre-Commit Checklist

Include this checklist verbatim in every delegated task that commits. The sub-agent must run all checks before `git commit`.

1. `git branch --show-current` — must start with `delegate/`. If it shows the feature branch, STOP and report without committing.
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

Steiner's sub-agent tools accept only `task`. When delegation is available, follow the briefing template in your system prompt, additionally including the pre-commit checklist from the Review-Fix Loop section.

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
