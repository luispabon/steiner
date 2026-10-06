---
name: implement
description: Execute an approved coding plan as serial-first implementation steps with tight scope control, planner-defined verification strategy, and isolated delegation/worktree execution. Use when planning is complete and the task should be implemented from the planner's artifacts.
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
- Run `git check-ignore -q .steiner/plans/`. Exit 0 means local-only planning artifacts: never stage or commit them. Otherwise commit them as described below.
- Treat `overview.md` and `plan.yaml` as immutable planner-owned inputs unless the user explicitly requests replanning.

## Execution Flow

Follow this sequence:

1. Validate input artifacts and branch state.
2. Check out the expected feature branch.
3. Load verification strategy from `overview.md`.
4. Create or resume compact `execution.md`.
5. Execute ready steps through delegation, splitting into bounded sub-tasks where useful. Dispatch one sub-agent per task; end the turn and act when results arrive. Implement directly only for `no_delegate` steps, stating why.
6. Run planned verification and fix failures.
7. Ask for manual verification only when the plan or risk requires it.
8. If planning artifacts are version-controlled, commit final executor state. Hand off to review.

Stop and report blockers instead of widening scope.

## Execution Artifact

`execution.md` is a compact state file under the planning folder. It should record only:

- active branch
- loaded verification strategy or explicit overrides
- current, completed, blocked, and skipped steps
- sub-agents used, their parent step ids, and any sub-task ids
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

Use `approach` as a starting point. The executor may adapt decomposition and implementation details when inspection or checks show the plan is incomplete or unsuitable, preserving intended behaviour, constraints, and acceptance criteria. Record meaningful deviations and reasons in `execution.md`. Scope, acceptance, shared-contract, or binding-decision changes require a revised decision or escalation before proceeding. Resolve `decisions` from Key Decision IDs in `overview.md`; they remain binding. Brief children with the current approach, evidence-backed adaptations, and resolved decisions. Children must report needed instruction changes, not silently redesign tasks.

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

## Splitting Steps into Sub-Agent Tasks

Plan-step scheduling is unchanged. Smaller models benefit from narrow tasks: each sub-task needs a concrete deliverable, declared write scope, and settled contracts. Preserve parent scope, constraints, acceptance criteria, `no_delegate`, and `delegate_profile`.

Within a ready step, prefer parallel tasks only with disjoint writes, no unmet dependencies or unresolved contracts, low coordination/merge risk, and meaningful time savings. Otherwise run serially. Children cannot see each other's unmerged work.

Track each sub-task's parent-linked id, state, and agent in `execution.md`. Apply delegation, review, merge, verification, and worktree rules per task. Mark the parent `implemented` only after all required tasks are implemented and merged; unlock dependents then, not earlier. Mark it `complete` only after required parent verification passes.

## Executor-Owned Work

The executor performs these actions directly using the native tool for each:

- artifact loading — `read` to load plan files; `grep` and `glob` to locate files
- `execution.md` creation and updates — `mutate`
- branch checkout, merge/conflict handling, cleanup — `bash` for git operations
- step scheduling and sub-agent dispatch
- verification orchestration — `bash` for running checks; `read` to inspect results
- reviewer handoff

Everything else is delegated.

### Implementation code restriction

The executor MUST NOT mutate **implementation-scoped files** (step `files`) via `mutate` or shell writes. All implementation edits and automated/manual verification fixes MUST go through `code` sub-agents, even tiny in-context edits. Direct edits violate this skill; the system prompt's local-edit permission is not a fallback.

This restriction does not apply to executor-owned artifacts (`execution.md`, branch operations). Steps marked `no_delegate` in the plan are also exempt.

Before any implementation action, ask: have I dispatched a sub-agent for this step? If no — stop, delegate.

## Delegation Model

Follow the system prompt's briefing template. Include the parent step id and goal, plus any sub-task id, in every delegated brief.

The feature branch is owned by the executor. Implementation-scoped code must be changed only by delegated sub-agents (see Implementation code restriction above). Every `code` sub-agent is automatically placed in a runtime-provisioned, runtime-verified worktree on a `delegate/` branch — the executor arranges nothing.

There is no inline fallback if delegation is unavailable: report a blocker. Only planned `no_delegate` steps may be applied inline.

If provisioning fails, the `code` call fails outright — that is a blocker to report, not a cue to work on the feature branch directly.

### Warm Follow-Up Policy

Prefer warm follow-up only for the same bounded task, scope, and live workspace with an available, resumable agent. Follow-ups are sequential. Keep the agent/worktree until its verification and correction loop ends. Cold-dispatch across tasks or after closure, worktree deletion, material lane/scope changes, or for independent/wider review. Resumability alone does not prove a worktree exists. Workflow handoffs are not safe continuation boundaries.

### Worktree Handling

Every `code` sub-agent runs in its own runtime-provisioned and runtime-verified git worktree on a `delegate/` branch under `.steiner/worktrees/`; you arrange nothing yourself.

1. Read `worktree_path` from the arrived result — a project-relative path (e.g. `.steiner/worktrees/<process>/<branch>/<agent>`) and the sole worktree locator returned to you. The branch name and any dirty-tree warnings are host-only and not returned; if you need the branch name, read it from the worktree itself: `git -C <worktree-path> branch --show-current`.
2. `follow_up` results also carry `worktree_path` for the same code agent, resolving to the same worktree as the initial `code` result.
3. After reviewing a step's result, merge the returned branch into the feature branch first, then remove the worktree and delete the branch, in that order: `git worktree remove <worktree-path>`, then `git branch -D <branch-name>` (from step 1).

### Delegation Steps

1. dispatch the scoped task to a `code` sub-agent
2. read the arrived result: `worktree_path`
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

## Strategic Guidance via `advisor`

If `advisor` is available, consult it before settling task decomposition and implementation risk. You may also consult it for evidence-backed adaptations and after unresolved verification failures before choosing a fix. Its guidance does not authorize scope or binding-decision changes.

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

Then call `workflow_handoff` with `next: review`, `target: .steiner/plans/FEATURE`, and optionally a one-line `message`; write no handoff prose before it. The call is mandatory, and the tool's prompt owns the user's accept/decline choice. On the result:

- **Accepted** — done; the review workflow starts in a cleared session.
- **Declined** or **unsupported** — say once `To review later, run /clear then /review .steiner/plans/FEATURE on an empty context.`, then stop; do not imply review has started or offer to continue.
- **Error** — fix the cause (outstanding sub-agents, missing artifact, bad path) and call again; never fall back to prose.
