# Oneshot Mode — Internals

User-facing documentation: [Oneshot Mode](oneshot.md).

## Architecture

### Run Identity and Provisioning

Each oneshot run receives a unique **run ID** (short slug derived from the task and a timestamp/nonce). The orchestrator derives a **feature slug** and creates:

- **Branch**: `oneshot/<slug>-<id>` (based on `origin/main`)
- **Worktree**: `.steiner/worktrees/oneshot-<id>` (git worktree from the shared `.git`)

The worktree and branch are immutable for the life of the run; concurrent runs never share a branch or worktree.

Per-run locking is atomic and short-lived — acquired during provisioning, held through run setup, and released after the manifest is written. Stale locks are detected and reclaimed via compare-and-swap during resume.

### Sandbox and Worktree

Two distinct concepts work together:

- **Sandbox writable-root**: remains the project root (`.`). This ensures `git status`, `git commit`, and `.steiner/config.yaml` work correctly.
- **Agent operational directory** (`workDir`): the worktree path (`.steiner/worktrees/oneshot-<id>`). All model interactions, tool invocations, and file mutations happen here.

The agent's prompt contexts reference the worktree path; tool schemas and results are scoped to the worktree. Phase agents resolve the project `AGENTS.md` from the oneshot worktree via `AssemblyOptions.ProjectAgentsPath` (`.steiner/worktrees/oneshot-<id>/AGENTS.md`); `ProjectRoot` remains the main checkout, so manifest paths, the sandbox writable-root, and skill discovery are unchanged. Between-phase state is carried by committed work in git and shared disk artifacts.

### Phase Loop: Plan → Implement → Review

Each phase is a fresh agent run with empty model context and a clean scrollback. Every phase receives its **phase orchestration prompt** as a system-level context source — embedded prompts (`internal/oneshot/prompts/{plan,implement,review}.md`) are loaded and delivered directly to the model as part of the static system context, bypassing byte budgets to ensure full delivery. All phases run in **delegated-child workflow mode** (no approval gating for mutations; the delegated task is pre-authorized). Cross-phase state is carried by:

1. **Disk artifacts**: `overview.md`, `plan.yaml`, `execution.md`, `review.md` (committed at phase boundaries)
2. **Git history**: each phase leaves a commit milestone
3. **Run manifest**: `.steiner/oneshot/<id>/run.json` (updated after each phase)

**Plan phase**:
- Task: analyze the request, explore the codebase, and produce a structured plan.
- Research: decided autonomously (no approval gate) using the same required-by-default criteria as the interactive plan skill — current/external/fast-moving, security-sensitive, or low-confidence areas trigger it. When required, it is delegated to the `research` tool; if no search backend is configured the tool is absent and the phase records a bounded assumption and continues. Findings persist to `research.md` when worth keeping.
- Output: `overview.md` (with `## Request`, `## Overview`, `## Key Decisions` — each with a stable ID, `## Tradeoffs`, `## Scope Boundaries`, `## Verification Strategy`, `## Decision Log`), `plan.yaml` (flat implementation steps; each step carries `decisions` referencing Key Decision IDs and an `approach` describing the concrete *how*), optional `research.md`, and a commit to the feature branch.
- Advisor findings: for each finding or concern raised during advisor consultation, the model must explicitly either apply the finding (modifying the plan) or reject it with a stated reason. Advancing without addressing every material finding is not permitted.
- Refinement: full advisor loops enabled if configured. A final `advisor` sanity check on the completed plan is mandatory before the commit (skipped only if the per-run advisor budget is exhausted); its note is recorded in `overview.md`.

**Implement phase**:
- Task: execute the plan's steps, make code changes, and validate with the plan's verification strategy.
- Input: reads `overview.md` (intent + verification strategy) and `plan.yaml` (flat step contract).
- Delegation: mandatory. The phase receives the orchestrator role from the system preamble (delegation is enabled for oneshot phases) — implementation-scoped edits and verification-failure fixes flow through delegated `code` sub-agents, one per step. Direct file mutation of implementation files is a violation, not a fallback; there is no inline execution tier. Steps marked `no_delegate: true` are the only inline exception, and the reason is recorded in `execution.md`. Rationalization via low ambiguity, small testable chunks, or cheap-feeling mutate calls are explicitly prohibited — these do not license skipping delegation.
- Output: `execution.md` (compact step/verification/handoff state), commits to the feature branch.
- Refinement: point-consults (advisor enabled but capped at 1-2 uses).

**Review phase**:
- Task: validate the implementation against the plan, drive findings to a verdict, and record the result.
- Input: reads `overview.md`, `plan.yaml`, `execution.md`, and all committed work.
- Findings: classified `blocking` / `non_blocking` / `informational`, mapping to a `fail` / `pass_with_notes` / `pass` status.
- Status reporting: every interim and final review status response shown in the transcript reports all currently known blocking and non-blocking findings and relevant informational notes, including on `fail`; later fix or verification passes may add, resolve, withdraw, or reclassify findings but must not silently omit previously reported ones. Verification gaps (checks that could not run because of the environment) and closeout notes (branch push state, PR/MR readiness; a local-only branch is a closeout note) are recorded separately from code findings.
- Delegation: mandatory, mirroring the implement phase — review-fix edits flow through delegated `code` sub-agents; direct file mutation of implementation files is a violation, not a fallback.
- Refinement: full advisor loops enabled. A final `advisor` sanity check on residual risk is mandatory before the verdict is marked (skipped only if the per-run advisor budget is exhausted); its note is recorded in `review.md`.
- Output: `review.md` (scope, status, findings, reruns, residual risk, advisor note), final commit. The engine then writes the structured `final-report.json` and runs closeout (PR push if `auto_pr` is enabled).

### Run Manifest

The manifest is a durable JSON record at `.steiner/oneshot/<id>/run.json`:

```json
{
  "run_id": "abc123",
  "slug": "refactor-auth",
  "task": "refactor the auth package to reduce complexity",
  "branch": "oneshot/refactor-auth-abc123",
  "worktree": ".steiner/worktrees/oneshot-abc123",
  "config_snapshot": {
    "plan_model": "default",
    "implement_model": "default",
    "review_model": "default",
    "auto_pr": false
  },
  "phases": [
    {
      "name": "plan",
      "status": "completed",
      "session_id": "sess_111",
      "commit": "abc1234...",
      "started_at": "2026-06-18T10:00:00Z",
      "completed_at": "2026-06-18T10:05:00Z"
    },
    {
      "name": "implement",
      "status": "completed",
      "session_id": "sess_222",
      "commit": "def5678...",
      "started_at": "2026-06-18T10:05:30Z",
      "completed_at": "2026-06-18T10:25:00Z"
    },
    {
      "name": "review",
      "status": "in_progress",
      "session_id": "sess_333",
      "started_at": "2026-06-18T10:25:30Z"
    }
  ],
  "created_at": "2026-06-18T10:00:00Z"
}
```

The manifest is used for resume logic, status reporting, and cross-phase bookkeeping.

### Resume Behavior

`steiner oneshot --resume <id>` validates and reclaims a run:

1. **Validation**: ensures the worktree and branch still exist (re-provisions from the branch if the worktree was removed).
2. **Lock reclamation**: acquires a fresh lock via CAS, releasing the stale one.
3. **State recovery**: reads the manifest and determines the first incomplete phase.
4. **Replay**: re-runs that phase from its start using on-disk artifacts and committed work. Completed phases are never re-run.

**Mid-implement resume**: If the implement phase fails before writing `execution.md` (e.g., the model hits an error or timeout before reaching the final execution artifact step), resuming the run re-enters the implement phase without requiring `execution.md` to exist. The model loads git history and committed work to identify what steps have already been implemented, then continues from the first incomplete step.

Worktrees are left in place after a run (including after interrupt) to allow inspection and resume. Cleanup is manual.

### Signal Handling

The orchestrator owns one interrupt context for the entire oneshot run. SIGINT (`Ctrl+C`):

1. Aborts the current phase without committing uncommitted changes.
2. Persists the run manifest (marking the current phase as incomplete).
3. Releases the per-run lock.
4. Leaves the worktree in place for inspection or resume.

### Closeout and PR Push

After the review phase completes with a passing verdict:

1. **If `auto_pr` is false**: the run ends and the branch remains local. The user can inspect and push manually.
2. **If `auto_pr` is true**: the orchestrator pushes the branch and opens a PR/MR automatically:
   - **GitHub**: uses `gh pr create`. If the PR already exists for the branch (a re-run of closeout), it falls back to `gh pr view` and reports the existing PR instead of failing.
   - **GitLab**: uses git push with merge request creation options.
   - **Azure Repos**: uses `az repos pr create`.

The PR/MR title comes from the first H1 (`# `) heading in `overview.md`, falling back to the task string if no H1 is present. The body is `overview.md` (with the H1 line removed) followed by a `---` separator and the full `review.md` content, verbatim — there is no commit list, since every forge already lists a PR's commits natively. The body is capped at 60,000 characters; an oversized body is truncated at a line boundary with a notice naming the planning folder.

### TUI Visibility and Interactive Behaviour

In interactive mode, oneshot runs are first-class and visible in real time:

1. **Phase dividers** in the scrollback, marked with phase name and timestamp.
2. **Status bar phase indicator** — a leading `phase · <name>` segment
   appears in the footer while a run is active (e.g. `phase · plan`).
3. **Sidebar section** — a small `Oneshot - <phase>` section appears in the
   sidebar while a run is active and is removed on completion.
4. **Live phase-agent output** — the phase agent's RunStarted, model
   chunks, tool calls, and RunFinished events are routed into the TUI
   transcript exactly like a normal run (the phase runtime's event sink
   is multiplexed onto the session sink).
5. **Full output retention** from all three phases in the scrollback
   history (no deletion or truncation between phases).
6. **Phase-scoped tool output** — tool results are annotated with their
   phase context.

After the run completes, all three phases remain visible in the
conversation history, allowing the user to review and fork or resume
from any point.

#### Steering-only composer

While a oneshot run is active, the composer is in **steering-only** mode.
The full slash-command surface is hidden; only a small allowlist of safe
view-toggle commands is honored, and every other input is sent to the
run as a steering message:

- Allowlist: `/exit`, `/thinking`, `/accent`.
- All other input (including `/oneshot <task>`, which would otherwise
  launch a second concurrent run) is routed to the run's steer channel.

The composer returns to the normal command surface on completion. The TUI
emits an `OneshotFinishedEvent` from the run goroutine when the run ends
(both success and error paths) and the `applyEvent` handler clears
`oneshotRunning`, `oneshotPhase`, the steer channel, and the chrome
fields.

### Concurrent Runs

Unique run IDs ensure that concurrent `oneshot` invocations never share:
- A branch name
- A worktree path
- A run manifest
- A lock

Concurrent runs can proceed in parallel without contention.

## Testing

Integration tests for oneshot behavior:

- **Run provisioning**: branch and worktree creation, manifest initialization.
- **Phase progression**: advancing through plan, implement, and review.
- **Resume logic**: recovering from partial runs, skipping completed phases.
- **Lock contention**: stale lock detection and reclamation.
- **Interrupt handling**: SIGINT persistence, graceful abort.
- **Concurrent runs**: parallel execution without contention.

Tests use git worktrees (real or mocked) and a temporary manifest directory.
