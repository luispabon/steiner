---
name: implement
description: Execute an approved coding plan as serial-first implementation steps with tight scope control, planner-defined verification strategy, and isolated Steiner delegation/worktree execution when available. Use when planning is complete and the task should be implemented from the planner's artifacts.
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
5. Execute ready implementation steps.
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
- `files`
- `constraints`
- `acceptance`
- `verification`

Optional fields:

- `depends_on`
- `parallel_group`
- `delegate_profile`

Do not infer missing implementation plans from `overview.md` alone.

## Scheduling

Execute serially by default.

Use `depends_on` only to block a step until real prerequisites are implemented.

Use `parallel_group` only when all of these are true:

- the plan explicitly marks the steps as independent
- the runtime can isolate work safely
- parallelism is likely to save meaningful time
- coordination and merge risk are low

If any condition is not met, run the steps serially in plan order.

Track step states as `pending`, `ready`, `running`, `implemented`, `blocked`, or `complete`.

Use `implemented` to unlock dependencies. Use `complete` only after required verification has passed.

## Executor-Owned Work

The executor owns orchestration:

- artifact loading
- branch checkout
- step scheduling
- `execution.md` updates
- temporary branch and worktree provisioning
- Steiner delegation dispatch
- merge/conflict handling
- temporary branch and worktree cleanup
- verification orchestration
- reviewer handoff

Implementation edits, verification-failure fixes, and manual-verification issue fixes belong to Steiner `code` delegates whenever safe isolated execution is available.

## Isolated Worktree Model

The feature branch is owned by the executor. Sub-agents must not work directly on it.

Safe isolated execution is available only when Steiner delegation tools are available, git worktrees and temporary branches can be created, and the repository state is clean enough to provision them safely.

When safe isolated execution is available, each implementation or fix pass runs in a dedicated worktree attached to a temporary branch created from the current feature branch.

The executor must:

1. create the temporary branch and worktree
2. delegate the scoped task inside that worktree
3. require the delegated agent to commit on the temporary branch
4. review the result against the step contract
5. merge it back to the feature branch
6. run required verification for that point in the flow
7. update `execution.md`
8. close the delegated agent
9. delete the worktree and merged temporary branch

Sub-agents must not merge, rebase, clean up executor-owned git state, or commit directly to the feature branch.

If safe isolated execution is unavailable, execute directly only as a fallback. Record the reason in `execution.md` and preserve the same step boundaries.

## Steiner Delegation

Use Steiner's specialised tools directly:

- `explore({"task": "..."})` for read-only discovery needed before implementation
- `code({"task": "..."})` for implementation or fix passes
- `verify({"task": "..."})` for check-only verification
- `plan({"task": "..."})` for bounded implementation sub-problem analysis
- `research({"task": "..."})` for approved current/external research
- `delegate({...})` only when no specialised profile fits

Specialised Steiner tools accept only `task`. Do not try to configure their prompts or models inline.

Every delegated task must be tight and self-contained. Include:

- the parent step id and goal
- relevant user intent and approved decisions
- scoped files, packages, or paths
- constraints, non-goals, and forbidden changes
- expected output and commit expectations
- verification to run or report

Do not pass broad conversation history or vague prompts. Do not make the delegated agent rediscover context the main agent already has.

## Verification Policy

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
- temporary branches/worktrees are cleaned up
- feature branch working tree is clean

Failed verification blocks reviewer handoff by default. Proceed to review with known blockers only if the user explicitly asks for review of a blocked implementation, and record that exception in `execution.md`.

If planning artifacts are version-controlled, commit the final executor state before handing off to review.

Use this handoff sentence exactly as written, with only the planning folder path substituted: `Please run /clear then /review .steiner/plans/FEATURE on an empty context.`

After delivering that sentence, call `workflow_handoff` with `next: review` and `target: .steiner/plans/FEATURE`. Do not imply the review workflow has already started. If the user accepts the handoff, context is cleared and the next workflow starts in the new session. If the user dismisses it, the tool returns a declined result and you must not assume continuation.
