---
name: implement
description: Execute an approved coding plan as serial-first implementation steps with tight scope control, planner-defined verification strategy, and isolated Steiner delegation/worktree execution. Use when planning is complete and the task should be implemented from the planner's artifacts.
---

# Coding Loop Executor

## Overview

Use this skill after the planner has produced an approved planning bundle. Read `overview.md` and the flat `plan.yaml`, execute the implementation steps, and keep changes aligned to the approved plan.

Treat the planning folder, feature branch, and repository state as the authoritative context at execution start.

## Input Contract

- Require one argument: the feature planning folder.
- Require `overview.md` and `plan.yaml`.
- Stop if artifacts are missing, conflict materially, or cannot be parsed.
- Derive the expected branch as `cl/YYYY-MM-DD_FEATURE_NAME`.
- Require the expected branch to exist and be clean before implementation starts.
- Check whether `.steiner/plans/` is gitignored by running `git check-ignore -q .steiner/plans/`. If exit code is 0, planning artifacts are local-only — do not stage or commit them at any point during this workflow. If exit code is non-zero, planning artifacts are version-controlled — commit them as described below.
- Treat `overview.md` and `plan.yaml` as immutable planner-owned inputs unless the user explicitly requests replanning.

## Execution Flow

Follow this sequence:

1. Validate input artifacts and branch state.
2. Check out the expected feature branch.
3. Load verification strategy from `overview.md`.
4. Create or resume compact `execution.md`.
5. Execute ready implementation steps — dispatch one sub-agent per step via the delegation model. Do not implement directly unless the step is marked `no_delegate`, in that case make sure to state explicitly why the change is not being delegated.
6. Run planned verification and fix failures.
7. Ask for manual verification only when the plan or risk requires it.
8. If planning artifacts are version-controlled, commit final executor state. Hand off to review.

Stop and report blockers instead of widening scope.

## Execution Artifact

`execution.md` is a compact state file under the planning folder. It should record only:

- active branch
- loaded verification strategy or explicit overrides
- current, completed, blocked, and skipped steps
- Steiner delegated agents used and their step ids
- verification commands and results
- deviations, blockers, and manual verification notes
- final reviewer handoff status

Do not maintain a verbose event log. Keep it sufficient for reviewer handoff.

## Plan Loading

Parse `plan.yaml` as a flat list of implementation steps.

The top level must be:

```yaml
steps:
  - id: step-1
    title: ...
```

Expected step fields:

- `id`
- `title`
- `scope`
- `decisions`
- `approach`
- `files`
- `constraints`
- `acceptance`
- `verification`

`approach` is authoritative for *how* a step is built: delegated sub-agents follow it rather than re-deriving design (names, signatures, locations, data shapes, edge/error handling). A step's `decisions` list cites Key Decision IDs in `overview.md`; resolve them there and treat them as binding constraints on the implementation. When a delegated step task is framed, pass the step's `approach` and the resolved text of its cited `decisions` into the sub-agent's context.

Optional fields:

- `depends_on`
- `parallel_group`
- `delegate_profile`
- `no_delegate`

Do not infer missing implementation plans from `overview.md` alone.

## Scheduling

Execute serially by default.

Use `depends_on` only to block a step until real prerequisites are implemented.

Use `parallel_group` only when all of these are true:

- the plan explicitly marks the steps as independent
- the steps touch disjoint file sets — every worktree branches from the parent's HEAD at dispatch time, so children cannot see each other's uncommitted work
- parallelism is likely to save meaningful time
- coordination and merge risk are low

If any condition is not met, run the steps serially in plan order.

Track step states as `pending`, `ready`, `running`, `implemented`, `blocked`, or `complete`.

Use `implemented` to unlock dependencies. Use `complete` only after required verification has passed.

## Executor-Owned Work

The executor performs these actions directly using the native tool for each:

- artifact loading — `read` to load plan files; `grep` and `glob` to locate files
- `execution.md` creation and updates — `mutate`
- branch checkout, merge/conflict handling, cleanup — `bash` for git operations
- step scheduling and Steiner delegation dispatch
- verification orchestration — `bash` for running checks; `read` to inspect results
- reviewer handoff

Everything else is delegated.

### Implementation code restriction

The executor MUST NOT call file-mutation tools (`mutate`, or `bash` for file writes) on **implementation-scoped files** — the files listed in step `files` fields. All implementation edits, verification-failure fixes, and manual-verification issue fixes MUST be performed by delegated Steiner `code` sub-agents. Doing so directly is a skill violation, not a fallback. Deliberate tightening of the routing threshold in your system prompt: the executor owns the feature branch, so even a small in-context edit must go through a `code` sub-agent — delegation is this workflow's entire purpose, not just its default.

This restriction does not apply to executor-owned artifacts (`execution.md`, branch operations). Steps marked `no_delegate` in the plan are also exempt.

Before any implementation action, ask: have I dispatched a sub-agent for this step? If no — stop, delegate.

## Delegation Model

The feature branch is owned by the executor. Implementation-scoped code must be changed only by delegated sub-agents (see Implementation code restriction above). Every `code` sub-agent is automatically placed in a runtime-provisioned, runtime-verified worktree on a `delegate/` branch — the executor arranges nothing.

There is no inline execution tier. If delegation itself is unavailable, stop and report a blocker. Exception: steps marked `no_delegate` in the plan are applied inline by the executor. (Same deliberate tightening as the Implementation code restriction above — the routing threshold's local-edit permission does not apply to implementation-scoped files in this workflow.)

If provisioning fails, the `code` call fails outright — that is a blocker to report, not a cue to work on the feature branch directly.

### Warm Follow-Up Policy

Resume a suitable warm agent before cold dispatch only when it remains available for the same bounded deliverable in the same still-live workspace and scope. Follow-ups are sequential. Do not close the agent or remove its worktree until the step's verification and correction loop finishes — warm follow-up within a step, cold dispatch across steps. Cold-dispatch after the agent is closed or its worktree is merged and deleted, even if the session reports resumable. A resumable session alone does not prove that an isolated worktree still exists. Use fresh delegation for unavailable or non-resumable sessions, material lane or scope changes, independent or wider review, or removed worktrees. Workflow handoffs are not safe continuation boundaries.

### Worktree Handling

Every `code` sub-agent runs in its own runtime-provisioned and runtime-verified git worktree on a `delegate/` branch under `.steiner/worktrees/`; you arrange nothing yourself.

1. Read `worktree_path` and `worktree_branch` from the delegation result.
2. Check `warnings` for entries noting uncommitted parent-tree changes the child could not see — every worktree branches from the parent's HEAD, so commit those on the feature branch before the next dispatch if the child needs them.
3. `follow_up` results do not repopulate `worktree_path`/`worktree_branch`; retain the values from the initial `code` result across any follow-up calls on the same agent.
4. After reviewing a step's result, merge the returned branch into the feature branch first, then remove the worktree and delete the branch, in that order: `git worktree remove <worktree-path>`, then `git branch -D <worktree-branch>`.

### Delegation Steps

1. dispatch the scoped task to a `code` sub-agent
2. read the result: `worktree_path`, `worktree_branch`, and any `warnings`
3. review the result against the step contract
4. merge the returned branch into the feature branch
5. run required verification for that point in the flow
6. update `execution.md`
7. close the delegated agent
8. remove the worktree and delete the merged branch (see Worktree Handling above)

Sub-agents must not merge, rebase, clean up executor-owned git state, or commit directly to the feature branch.

### Pre-Commit Checklist

Include this checklist verbatim in every delegated task that commits. The sub-agent must run all checks before `git commit`.

1. `git branch --show-current` — must start with `delegate/`. If it shows the feature branch, STOP and report without committing.
2. `git status` — must show only files within the declared scope as modified. If unexpected files appear, STOP and report.

If any check fails, the sub-agent must not commit. It must report the mismatch and let the executor recover.

## Steiner Delegation

Steiner's sub-agent tools accept only `task`. When delegation is available, follow the briefing template in your system prompt, additionally including the parent step id and goal, the step's resolved cited decisions, and the pre-commit checklist from the Delegation Model section.

If an advisor tool is available, consult it before locking an implementation approach
and again after an unresolved verification failure before choosing the next fix path.

## Verification Policy

Before running `make check` or `golangci-lint run`, run `golangci-lint cache clean` to avoid false positives from stale cache entries pointing at deleted worktree paths.

Use the narrowest meaningful verification that gives sufficient confidence.

Prefer:

1. repo-mandated checks for the affected area
2. step-specific verification from `plan.yaml`
3. cheap planner-recorded checks scoped to changed files or subsystem
4. broader checks only when risk or repo policy requires them

By default, defer automated verification until all implementation steps are implemented. Run earlier verification only when the plan, repo policy, or risk requires it.

When safe fix mode is available and appropriate, prefer fix mode over check-only mode.

Fix mode is safe only when it is scoped to touched or relevant files, non-destructive, compatible with repo policy and approval requirements, and its changes can be reviewed before commit.

Failures must be fixed or reported as blockers. Do not widen scope for unrelated pre-existing warnings.

## Handoff

Reviewer handoff requires:

- all planned steps are implemented
- required verification is passing
- `execution.md` is updated with compact final state
- `delegate/` branches and worktrees returned by delegated steps are merged and cleaned up
- feature branch working tree is clean

Failed verification blocks reviewer handoff by default. Proceed to review with known blockers only if the user explicitly asks for review of a blocked implementation, and record that exception in `execution.md`.

If planning artifacts are version-controlled, commit the final executor state before handing off to review.

Use this handoff sentence exactly as written, with only the planning folder path substituted: `Please run /clear then /review .steiner/plans/FEATURE on an empty context.`

After delivering that sentence, call `workflow_handoff` with `next: review` and `target: .steiner/plans/FEATURE`. Do not imply the review workflow has already started. If the user accepts the handoff, context is cleared and the next workflow starts in the new session. If the user dismisses it, the tool returns a declined result and you must not assume continuation.
