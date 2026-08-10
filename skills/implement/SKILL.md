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
5. Execute ready implementation steps — dispatch one sub-agent per step via the delegation model. Do not implement directly unless the step is marked `no_delegate`, in that case make sure to state explicitly why the change is not being delegated
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
- the runtime can isolate work safely
- parallelism is likely to save meaningful time
- coordination and merge risk are low

If any condition is not met, run the steps serially in plan order.

Track step states as `pending`, `ready`, `running`, `implemented`, `blocked`, or `complete`.

Use `implemented` to unlock dependencies. Use `complete` only after required verification has passed.

## Executor-Owned Work

The executor acts only as an orchestrator. It performs these actions directly using the native tool for each — do not route through `bash` when a dedicated tool exists:

- artifact loading — `read` to load plan files; `grep` and `glob` to locate files
- `execution.md` creation and updates — `mutate`
- branch checkout, worktree provisioning, merge/conflict handling, cleanup — `bash` for git operations
- step scheduling and Steiner delegation dispatch
- verification orchestration — `bash` for running checks; `read` to inspect results
- reviewer handoff

Everything else is delegated.

### Implementation code restriction

The executor MUST NOT call file-mutation tools (`mutate`, or `bash` for file writes) on **implementation-scoped files** — the files listed in step `files` fields. All implementation edits, verification-failure fixes, and manual-verification issue fixes MUST be performed by delegated Steiner `code` sub-agents. Doing so directly is a skill violation, not a fallback.

This restriction does not apply to executor-owned artifacts (`execution.md`, worktree provisioning, branch operations). Steps marked `no_delegate` in the plan are also exempt.

Before any implementation action, ask: have I dispatched a sub-agent for this step? If no — stop, provision, delegate.

## Delegation Model

The feature branch is owned by the executor. Implementation-scoped code must be changed only by delegated sub-agents (see Implementation code restriction above). The executor prefers the highest available delegation tier:

1. **Isolated delegation** (preferred): sub-agent works in a dedicated worktree on a temporary branch. Provides full isolation from the feature branch.
2. **Direct delegation** (fallback): sub-agent works directly on the feature branch. Used when worktrees are unavailable or provisioning fails.

There is no inline execution tier. If delegation itself is unavailable, stop and report a blocker. Exception: steps marked `no_delegate` in the plan are applied inline by the executor.

Prefer isolated delegation. Fall back to direct delegation only when `git worktree add` fails, worktree provisioning checks fail, or sub-agent dispatch returns an error for the worktree path. A judgment that isolation is unnecessary or that the edits are simple does not justify skipping to direct delegation — only concrete errors do.

### Worktree Provisioning

Always create worktrees under `.steiner/worktrees/` inside the project root. Do not use `/tmp` or other system temporary directories — they may be sandboxed and silently fail.

After running `git worktree add`, verify the directory actually exists:

1. Run `ls -d <worktree-path>` to confirm the directory was created.
2. Run `git -C <worktree-path> branch --show-current` to confirm it is on the expected temporary branch.
3. If either check fails, prune the worktree entry with `git worktree remove <worktree-path>` and fall back to direct delegation.

### Isolated Delegation Steps

When using isolated delegation, the executor must:

1. create the temporary branch and worktree under `.steiner/worktrees/`
2. verify the worktree is accessible (see provisioning checks above)
3. delegate the scoped task inside that worktree
4. require the delegated agent to commit on the temporary branch
5. review the result against the step contract
6. merge it back to the feature branch
7. run required verification for that point in the flow
8. update `execution.md`
9. close the delegated agent
10. delete the worktree and merged temporary branch

Sub-agents must not merge, rebase, clean up executor-owned git state, or commit directly to the feature branch.

### Direct Delegation Steps

When using direct delegation, the executor must:

1. delegate the scoped task on the feature branch
2. require the delegated agent to commit on the feature branch
3. review the result against the step contract
4. run required verification for that point in the flow
5. update `execution.md`
6. close the delegated agent

### Pre-Commit Checklist

Include the appropriate checklist verbatim in every delegated task that commits. The sub-agent must run all checks before `git commit`.

**Isolated delegation mode:**

1. `git branch --show-current` — must equal the temporary branch name given in the task. If it shows the feature branch, STOP and report without committing.
2. `git rev-parse --show-toplevel` — must equal the worktree path given in the task. If it shows a different path, STOP and report without committing.
3. `git status` — must show only files within the declared scope as modified. If unexpected files appear, STOP and report.

**Direct delegation mode:**

1. `git branch --show-current` — must equal the feature branch name given in the task. If it shows a different branch, STOP and report without committing.
2. `git status` — must show only files within the declared scope as modified. If unexpected files appear, STOP and report.

If any check fails, the sub-agent must not commit. It must report the mismatch and let the executor recover.

## Steiner Delegation

Use Steiner's specialised tools directly:

- `explore({"task": "..."})` for read-only discovery needed before implementation
- `code({"task": "..."})` for implementation or fix passes
- `sanity_check({"task": "..."})` for check-only verification
- `evaluate({"task": "..."})` for bounded implementation sub-problem analysis
- `research({"task": "..."})` for approved current/external research

Specialised Steiner tools accept only `task`. Do not try to configure their prompts or models inline.

If an advisor tool is available, consult it before locking an implementation approach
and again after an unresolved verification failure before choosing the next fix path.

Every delegated task must be tight and self-contained. Include:

- the parent step id and goal
- relevant user intent and approved decisions
- scoped files, packages, or paths
- constraints, non-goals, and forbidden changes
- expected output and commit expectations
- verification to run or report
- the appropriate pre-commit checklist from the Delegation Model section

Do not pass broad conversation history or vague prompts. Do not make the delegated agent rediscover context the main agent already has.

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
- temporary branches/worktrees are cleaned up
- feature branch working tree is clean

Failed verification blocks reviewer handoff by default. Proceed to review with known blockers only if the user explicitly asks for review of a blocked implementation, and record that exception in `execution.md`.

If planning artifacts are version-controlled, commit the final executor state before handing off to review.

Use this handoff sentence exactly as written, with only the planning folder path substituted: `Please run /clear then /review .steiner/plans/FEATURE on an empty context.`

After delivering that sentence, call `workflow_handoff` with `next: review` and `target: .steiner/plans/FEATURE`. Do not imply the review workflow has already started. If the user accepts the handoff, context is cleared and the next workflow starts in the new session. If the user dismisses it, the tool returns a declined result and you must not assume continuation.
