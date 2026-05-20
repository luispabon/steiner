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
- Derive the expected branch as `cl/YYYY-MM-DD_FEATURE_NAME`.
- Require the expected branch to exist and be clean before review starts, except for safe reviewer artifact initialization.
- Treat planner and executor artifacts as immutable inputs unless the user explicitly requests replanning or artifact correction.

## Review Flow

Follow this sequence:

1. Validate artifacts and branch state.
2. Check out the expected feature branch.
3. Create or resume compact `review.md`.
4. Run the review pass.
5. If blocking findings exist, ask approval for one consolidated fix plan.
6. Run approved review-fix work through isolated sub-agents when available.
7. Rerun relevant checks.
8. Repeat only if new blocking findings remain.
9. Mark final status.
10. Offer closeout actions: planning-doc cleanup and PR/MR creation.

Stop and report blockers instead of widening scope.

## Review Artifact

`review.md` is a compact final-state file under the planning folder. It should record:

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

- plan and scope adherence
- obvious bugs or regressions
- missing or weak verification
- correctness against accepted intent
- maintainability issues that materially affect correctness or future work

Review touched files and directly adjacent regression-risk areas. Do not broadly re-review unrelated code.

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

When safe isolated execution is available, run the approved fix pass in a dedicated temporary branch and worktree. The reviewer owns provisioning, merge, verification, cleanup, and `review.md` updates.

The review-fix sub-agent must:

- receive only the approved findings, fix plan, relevant files, constraints, and verification strategy
- commit its changes on the temporary branch
- avoid unrelated cleanup or scope expansion
- not merge, rebase, or clean up reviewer-owned git state

If safe isolated execution is unavailable, direct fixes are allowed only as a fallback and must keep the same approved fix-pass boundary.

Review-fix work is sequential. Do not parallelize it.

## Steiner Delegation

When running in Steiner, use specialised tools directly:

- `explore({"task": "..."})` for targeted review discovery
- `code({"task": "..."})` for approved review-fix passes
- `verify({"task": "..."})` for check-only verification
- `plan({"task": "..."})` for bounded analysis of a review uncertainty
- `research({"task": "..."})` only for approved current/external research
- `delegate({...})` only when no specialised profile fits

Specialised Steiner tools accept only `task`. The reviewer must provide a tight, self-contained task with known context, relevant files, approved decisions, constraints, expected output, and non-goals.

Outside Steiner, spawn the cheapest capable sub-agent/profile available.

## Verification After Fixes

Reuse the verification strategy in `overview.md` by default. Rerun the narrowest checks that cover the fixes and any affected acceptance criteria.

If verification fails, either run another approved review-fix pass or report a blocker. Do not silently downgrade failures.

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

- delete `.project_planning/YYYY-MM-DD_FEATURE_NAME/`
- commit the deletion unless the user explicitly asks otherwise
- record the cleanup result in `review.md` before deletion, or include the cleanup result in the closeout message if `review.md` is deleted

If PR/MR creation is also requested, preserve any needed summary details before deleting planning docs.

## PR Or MR Preparation

Use commit messages and loop artifacts for PR/MR summaries. Do not broad-diff the branch against `origin/main` by default.

Build the PR/MR body from:

- the `## Overview` section of `overview.md`
- non-merge commit messages created for the loop
- relevant deviations, residual risks, and final status from `execution.md` and `review.md`

Inspect targeted diffs only if commit messages or artifacts are missing, vague, contradictory, or clearly insufficient.

Detect the remote provider from the current branch tracking remote or `origin`. Use only known provider flows:

- GitHub: push if needed, then `gh pr create --title <title> --body <body> --base <target> --head <branch>`
- GitLab: `git push -o merge_request.create -o merge_request.target=<target> <remote> <branch>`
- Azure DevOps: push if needed, then `az repos pr create --title <title> --description <body> --source-branch <branch> --target-branch <target>`
- Bitbucket or unknown provider: report unsupported automatic PR/MR creation

Ask for confirmation before pushing or creating a PR/MR. Do not dump the full PR/MR body unless the user asks.

## Completion

The loop is complete when review has passed and the user has either accepted, declined, or completed closeout actions. Unsupported PR/MR creation does not invalidate the reviewed work; it only prevents automatic PR/MR creation.
