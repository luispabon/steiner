# Overview — `/oneshot` autonomous end-to-end command

## Request

GitHub issue #227: Add a `/oneshot` command that takes a user request from intent
to completed implementation without further user interaction. It combines the
useful behaviours of plan, implement, and review into three distinct phases
(planning, implementation, review), runs unattended, reuses delegation and
advisor behaviours, provisions its own worktree, supports multiple concurrent
runs without corruption, clears context between phases, and ends with a concise
structured final report (or a clean failure report).

## Overview

`oneshot` is a **first-class autonomous orchestration mode built into steiner**,
not a slash-command skill riding `workflow_handoff`, and not an agent
orchestrating sub-agents (that would violate the no-nesting invariant). A new
headless engine in `internal/oneshot` drives steiner's own agent loop **three
times** — plan → implement → review — each as a fresh agent run with **fresh
model context**, built against a **runtime re-pointed at a dedicated worktree**.

Within a phase the agent still uses normal one-level delegation; because the
orchestrator is machinery (not an agent), there is no nesting.

The engine is wired to **both** a CLI subcommand (`steiner oneshot "<task>"`) and
a TUI command (`/oneshot <task>`) in this iteration.

High-level flow:

1. **Provision run identity + worktree.** Derive a short run id and a feature
   slug. Create branch `oneshot/<slug>-<id>` and worktree
   `.steiner/worktrees/oneshot-<id>` from `origin/main`. Take a per-run lock.
   Unique id per run makes concurrent `/oneshot` runs safe — they never share a
   branch or worktree.
2. **Run plan phase** (fresh run, autonomous prose, model context empty, workDir =
   worktree). Produces `overview.md` + `plan.yaml` under the worktree planning
   folder. Advisor-driven refinement loop.
3. **Boundary check** (mechanical): required artifacts present, working tree clean.
4. **Run implement phase** (fresh run). Sub-agents work **directly in the shared
   worktree**. Model commits per validated unit of work. Advisor point-consults.
5. **Boundary check** (mechanical).
6. **Run review phase** (fresh run). Unified fix + advisor loop drives **all**
   findings — blocking and non-blocking — to green and advisor sign-off, bounded.
7. **Closeout**: write the structured final report; leave the branch/worktree in
   place. If `oneshot.auto_pr` is enabled in config, also push the branch and open
   a PR/MR (see Closeout & auto-PR below); otherwise no outward-facing action.

Cross-phase state is carried by **disk artifacts** (`overview.md`, `plan.yaml`,
`execution.md`, the report) plus **git history** — exactly the carrier the
existing handoff flow already relies on across context clears. The orchestrator
owns the **worktree lifecycle, branch identity, lock, and cleanup**; the **model
performs commits** (driven by phase prose) inside the worktree.

### Run manifest & resume

Every run records a durable **run manifest** (e.g.
`.steiner/oneshot/<id>/run.json`) holding: run id, slug, task, branch, worktree
path, model config snapshot, current phase, per-phase status, per-phase session
ids, and commit milestones. The manifest is updated at each phase transition and
at commit milestones, making it the authoritative resume record.

**Resume is phase-level.** `steiner oneshot --resume <id>` (CLI) and a TUI resume
entry re-open a run from its manifest: the orchestrator validates the
worktree/branch still exist (re-provisioning the worktree from the branch if it
was removed), clears any **stale lock**, determines the first incomplete phase,
and **re-runs that phase from its start** using the on-disk artifacts and already
committed work as input. Completed phases are not re-run. Mid-phase resume
(restoring a phase agent's in-progress conversation turn-by-turn) is deliberately
not attempted — the artifact-carrier model makes a clean phase restart both
simpler and more robust. A run listing (`steiner oneshot --list`) surfaces
resumable runs.

Stale-lock detection: the lock records owner/timestamp; a lock whose owner is gone
or whose heartbeat is stale is treated as reclaimable on resume.

### Closeout & auto-PR

Closeout always writes the report and leaves the branch/worktree. When
`oneshot.auto_pr` is enabled (default **false**), closeout additionally performs an
outward-facing PR/MR step after a passing review: detect the remote provider, push
the branch, and open a PR/MR with a body assembled from `overview.md`'s overview
section, the loop's non-merge commit messages, and the final review outcome/risks.
Provider flows mirror the existing review/pull-request skill (GitHub `gh`, GitLab
push options, Azure `az`; unsupported providers report cleanly). Enabling the
setting is the user's explicit, durable authorization for the push/PR.

### Autonomy & safety

- No interactive approval after invocation. An **auto-approver scoped to the
  worktree** satisfies the mutation approval gate; the bash sandbox remains the
  guardrail.
- Phase prose has **no user-approval gates** and makes bounded, recorded
  assumptions instead of pausing.
- Every loop has hard termination bounds (advisor budget + round cap). Residual,
  unresolved concerns are recorded in the report rather than hanging.
- If a boundary contract fails (missing artifact, dirty tree) or a phase cannot
  proceed safely, the run **stops cleanly** and reports the phase reached and the
  state left behind.

### Sandbox, git worktree, and signals

Three execution constraints surfaced in advisor review that the implementation
must honour:

- **Sandbox writable-root vs agent workDir are decoupled.** A git worktree's
  `.git` is a pointer to `<mainrepo>/.git/worktrees/<id>` and objects live in the
  shared `<mainrepo>/.git/objects`, both *outside* the worktree subtree. If the
  sandbox made only the worktree writable, `git commit`/`git status` would fail
  read-only — breaking the model-driven commit mechanism. Therefore the **sandbox
  writable-root stays the project root** (which contains both the worktree subdir
  and the parent `.git`), while the **agent's operational workDir = the worktree**
  (prompt paths, path policy, tool cwd). These are two separate concepts; the
  step-1 refactor must split them rather than equate "re-pointed runtime" with
  "sandbox workspace = worktree."
- **`.steiner` presence in a fresh worktree.** A worktree checked out from
  `origin/main` does not contain the runtime-created, git-ignored `.steiner/`
  scaffolding. The orchestrator runs the equivalent of `ensureSteinerProjectDir`
  against the worktree immediately after `git worktree add`, writes planning
  artifacts under the worktree's `.steiner/plans/...` (so they are ignored and
  consistent with the existing handoff path convention), and the "tree clean"
  boundary check ignores `.steiner/`.
- **Single signal owner.** The orchestrator owns one interrupt context for the
  whole run; phase runs receive a child context and must not install their own
  signal handlers. SIGINT means: abort the current phase, persist the manifest,
  release the lock, and leave the worktree in place — never a half-released,
  stale-locked state.

### Steering during autonomous runs

Steering (`SteerCh` — between-turn user messages, non-blocking) **remains
available** so the user can nudge a running phase ("focus on X", "skip that")
without stopping it. The orchestrator owns the steer channel across the run and
wires it into each fresh phase run. Steering is strictly opportunistic: the run
**never blocks waiting** for it — an AFK user changes nothing.

This is separate from, and must not break, the **no-stop-to-ask autonomy
contract**. The model must never pause for user input. Three mechanisms enforce
that together:

1. **Phase prose** forbids clarifying questions and requires bounded, recorded
   assumptions (no approval gates).
2. The **worktree-scoped auto-approver** means mutation approval never pauses.
3. **No `workflow_handoff` modal** — phase transitions are orchestrator-driven, not
   user-gated.

So steering is the only user→model channel during a run, and it is non-blocking by
construction. Steering messages land in the phase session transcripts, so they are
visible in after-the-fact review. (Steering is primarily a TUI affordance; the CLI
subcommand runs with no steer source by default — the autonomy contract is
identical either way.)

### Advisor (quality backbone)

The advisor is a per-run, model-facing, advisory-only tool with a per-run budget
(`MaxUsesPerRun`), default-off. Because each phase is a fresh run, **each phase
gets its own advisor budget**. The orchestrator **force-enables** the advisor for
its phase runs. Phase prose mandates advisor use at key moments and treats it as a
**loop driver**, not a one-shot check:

- **plan**: after drafting `plan.yaml`, consult → revise artifacts → re-consult,
  until sign-off or bound. (round cap 3, budget 4)
- **implement**: point-consult before locking the approach and after an
  unresolved verification failure. (budget 3)
- **review**: interleaved fix + advisor loop until checks are green AND the
  advisor has no remaining concerns, or bound. (round cap 3, budget 4)

These round caps and per-phase advisor budgets live as **named constants in
`internal/oneshot`** (not config) for this iteration. The orchestrator threads a
per-phase `AdvisorConfig` override (`Enabled: true`, per-phase budget) into
registry construction **without mutating the global config**.

### Model configuration

Three independent tiers, all with existing precedent; oneshot adds only the first:

1. **Per-phase orchestrating model** (new) — a dedicated `oneshot` config block
   with a `models` map (`plan` / `implement` / `review` → alias), fallback to
   `DefaultModel`, validated against `Config.Models`. Mirrors
   `workflow_handoff.models`. The same `oneshot` block also holds `auto_pr`
   (bool, default false).
2. **Delegation models** (unchanged) — sub-agents keep `SubAgent.Agents[type].Model`.
3. **Advisor model** (unchanged) — `AdvisorConfig.Model` across all phases.

### Visibility of prior phases

For an AFK user returning at review-end:

- **Durable**: each phase is its own **persisted session** (full lineage). A new
  **run-id / group field on `Session`** (and the index) links the three phases so
  the picker can present them as one run. The final report lists per-phase session
  ids. Works for both TUI and CLI runs.
- **Live (TUI)**: on a phase transition the **on-screen transcript is kept** with a
  `── Phase N: <name> ──` divider; only the **model context** resets. This
  deliberately diverges from `workflow_handoff`'s wipe-on-accept.

### Final report

Concise structured report containing: requested task summary; changes made; files
changed; validation run and results; review outcome; review-fix iterations;
assumptions made; known limitations / risks / unresolved issues; whether the task
is considered complete. On failure: what was attempted, what blocked progress, and
whether any changes were left behind. Written to the worktree planning folder and
emitted as the final message.

## Key Decisions

- **Built-in orchestration mode, not skill + `workflow_handoff`.** Seamless,
  first-class, avoids the modal/user-gated rails. (user directive)
- **steiner drives 3 phase runs; not an agent orchestrating sub-agents.** Respects
  the no-nesting invariant; the orchestrator is machinery. (user directive)
- **New autonomous phase prose**, distinct from the existing `skills/*/SKILL.md`;
  embedded in `internal/oneshot`, not exposed as user slash-command skills.
- **In-process, re-pointed runtime per phase.** Each phase builds a fresh
  runtime/registry/executor/sandbox/prompt bound to the worktree workDir. Chosen
  over subprocess-per-phase for tighter integration, shared event stream, and one
  report. (user choice)
- **Both CLI subcommand and TUI command** wired this iteration over a shared
  engine. (user choice)
- **One worktree per run from `origin/main`; sub-agents work directly in it;
  orchestrator owns worktree lifecycle.** (issue requirement)
- **Model performs commits per validated unit of work, driven by phase prose;**
  orchestrator does not gate or auto-commit. (user directive)
- **Mechanical boundary contracts** (artifacts present, tree clean); the **review
  phase is the cleanup gate** — no hard verification gate at implement→review.
  Review drives all blocking and non-blocking findings to resolution. (user choice)
- **Advisor force-enabled per phase and used as a loop driver** in plan and review,
  with point-consultations in implement; per-phase budgets. (user directive)
- **Termination guaranteed** by advisor budget + explicit round caps; residuals
  recorded in the report.
- **Per-phase persisted sessions linked by a new `Session` run-id/group field;**
  TUI keeps scrollback with phase dividers; only model context resets. (user choice)
- **Dedicated `oneshot.models` config block** for per-phase models; delegation and
  advisor model tiers unchanged. (user choice)
- **Resume is in scope, at phase granularity.** A durable run manifest is the
  resume record; resume re-runs the first incomplete phase from its start using
  on-disk artifacts and committed work. Mid-phase resume is out of scope. (user)
- **Steering stays available but never blocks.** The steer channel is wired into
  each phase run so the user can nudge it; the no-stop-to-ask autonomy contract is
  enforced by phase prose + auto-approver + no handoff modal. (user)
- **Auto-PR is config-gated** via `oneshot.auto_pr` (default false). When enabled
  it is the user's durable authorization for the push/PR at closeout; otherwise
  closeout stops at the report and leaves the branch. (user)

## Tradeoffs

- **In-process vs subprocess phases.** Subprocess-per-phase (`steiner --exec` in
  the worktree) would give trivial context isolation and a simple workDir story,
  but heavier IPC and harder live streaming/aggregation in the TUI. We chose
  in-process; the real cost is larger than "parameterize workDir": it requires
  decoupling the sandbox writable-root from the agent workDir (see Sandbox, git
  worktree, and signals), exposing a PhaseRunner factory that rebinds
  registry/executor/sandbox/prompt/advisor per phase, and single-owner signal
  handling. This is the load-bearing, regression-prone part of the build.
- **Orchestrator-gated commits vs model-driven commits.** A Go `commit` tool gated
  on verification would make git fully deterministic, but the user wants commit
  timing to be a model judgment expressed in prose. We keep git mechanics owned by
  the worktree lifecycle but let the model decide when to commit.
- **Hard verification gate at implement→review vs review-as-cleanup-gate.** A hard
  gate matches the current executor handoff and fails fast, but the user wants the
  review phase to drive everything green. We removed the hard gate; risk is review
  chasing implementation failures, bounded by the review fix-loop cap.
- **Reuse `workflow_handoff.models` vs dedicated `oneshot.models`.** Reuse means
  zero new config but conflates interactive and autonomous intent and has no `plan`
  target. We chose a dedicated block.
- **Wipe TUI scrollback (consistent with handoff) vs keep with dividers.** Keeping
  scrollback diverges from existing behaviour but is required for live AFK
  visibility; model context still resets, so the autonomy contract holds.
- **New `Session` group field vs title convention.** Title-only avoids a schema
  change but gives no real grouping; we add a lightweight field.
- **Phase-level vs mid-phase resume.** Mid-phase resume would restore a phase
  agent's conversation turn-by-turn (sessions persist lineage), but adds large
  complexity around partial commits and in-flight delegation. Phase-level resume
  re-runs the incomplete phase from on-disk artifacts + committed work — simpler
  and robust. Cost: an interrupted phase's uncommitted progress is discarded on
  resume (committed units are kept).
- **Auto-PR always-off vs config-gated.** A hard always-off default is safest but
  blocks fuller autonomy. A config setting (`auto_pr`, default false) keeps the
  safe default while letting users grant durable authorization for the push/PR.

## Scope Boundaries

**In scope**

- `internal/oneshot` orchestrator engine (run identity, worktree provisioning +
  lifecycle + lock + cleanup, per-phase run construction, boundary contracts,
  report assembly).
- Parameterizing runtime/registry/executor/sandbox/prompt construction by
  `workDir` so a phase run can bind to the worktree (the core refactor).
- New autonomous phase prose (plan / implement / review) embedded in
  `internal/oneshot`.
- Worktree-scoped auto-approver.
- Wiring the steer channel into each phase run (steering stays available; runs
  never block on it).
- Advisor force-enable + per-phase budgets + loop usage via prose.
- `oneshot` config block (`models` map + `auto_pr`) + validation + defaults.
- Durable run manifest (write/update/read) + phase-level resume + stale-lock
  handling + run listing; CLI `--resume <id>` / `--list` and a TUI resume entry.
- Config-gated auto-PR closeout step (provider detection, push, PR/MR body
  assembly) reusing existing provider flows.
- New `Session` run-id/group field (+ index) and grouped picker awareness.
- TUI: `/oneshot` command, keep-scrollback-with-dividers on phase transition,
  live phase indication.
- CLI: `steiner oneshot "<task>"` subcommand.
- Structured final report (success + failure shapes).
- Tests (engine, worktree/identity, boundary contracts, config, session field,
  report) and docs (README feature section, a `docs/` page, CONFIGURATION updates).

**Out of scope**

- Changing the existing interactive `plan` / `implement` / `review` /
  `pull-request` skills or `workflow_handoff`.
- Per-phase advisor models (single `AdvisorConfig.Model` is reused).
- Nested sub-agents or multi-level delegation.
- **Mid-phase** resume (restoring a phase agent's in-progress conversation
  turn-by-turn). Resume is phase-level only.

## Verification Strategy

From `CLAUDE.md` (Go 1.25). Run targeted checks first, broaden before finalizing.

| Command | Purpose | Cost | Notes |
|---|---|---|---|
| `gofmt -w <files>` | format | cheap | after every Go edit; prefer fix mode |
| `goimports -w <files>` | imports | cheap | after Go edits |
| `go build ./...` | compile | cheap–medium | after structural changes |
| `go vet ./...` | static checks | medium | |
| `go test ./internal/oneshot/... -run <Name>` | targeted unit tests | cheap | primary loop during dev |
| `go test ./internal/config/... ./internal/session/... ./internal/interactive/... ./cmd/steiner/...` | affected packages | medium | for config/session/TUI/CLI changes |
| `go test ./...` | full suite | medium–expensive | before finalizing |
| `go test -race ./...` | race detection | expensive | concurrency-sensitive (parallel runs, locks) — run before finalizing |
| `golangci-lint run ./...` | lint | medium | run `golangci-lint cache clean` first if worktrees were used |
| `govulncheck ./...` | vulnerabilities | medium | |
| `make check` | repo-mandated gate | expensive | **required before finalizing Go changes** |
| `make build-binaries` | build artifacts | expensive | if CLI wiring changes warrant it |

Prefer safe fix mode (`gofmt -w`, `goimports -w`) scoped to touched files. If a
check cannot run, report the exact command and failure. `make check` is the
mandated final gate.

## Decision Log

- 2026-06-18 — Mechanism: built-in orchestration mode, not skill + handoff. (user)
- 2026-06-18 — Not agent-orchestrating-sub-agents; steiner drives 3 phase runs;
  no-nesting preserved. (user)
- 2026-06-18 — Phase prose is new/autonomous, not the existing skills. (user)
- 2026-06-18 — Phase execution: in-process, re-pointed runtime per phase. (user)
- 2026-06-18 — Entry surfaces: both CLI subcommand and TUI command this iteration. (user)
- 2026-06-18 — Commits are model-driven via prose; orchestrator owns worktree
  lifecycle, not commit trigger. (user)
- 2026-06-18 — No hard verification gate at implement→review; review drives all
  blocking + non-blocking findings to resolution. (user)
- 2026-06-18 — Advisor force-enabled per phase; loop driver in plan and review;
  point-consults in implement; bounds guarantee termination. (user)
- 2026-06-18 — Visibility: per-phase persisted sessions + new `Session`
  run-id/group field; TUI keeps scrollback with dividers, model context resets. (user)
- 2026-06-18 — Model config: dedicated `oneshot.models` block; delegation and
  advisor tiers unchanged. (user)
- 2026-06-18 — Resume: in scope at phase granularity, via a durable run manifest;
  mid-phase resume out of scope. (user)
- 2026-06-18 — Steering remains available (non-blocking) during runs; autonomy
  contract (no stop-to-ask) enforced by prose + auto-approver + no handoff modal. (user)
- 2026-06-18 — Auto-PR: config-gated `oneshot.auto_pr` (default false); enabling
  is durable authorization. (user)
- 2026-06-18 — Research: not needed (repo-local Go, stable git semantics). (planner, accepted)
- 2026-06-18 — Advisor (architect) sanity check run on overview + plan; verdict
  has-gaps. Folded in: sandbox-root/workDir decoupling, single signal owner,
  atomic lock + worktree-prune races, `.steiner` scaffolding in fresh worktree +
  tree-clean ignore, advisor force-enable threading + budget constants, engine→TUI
  event contract, group-carrying RotateSession, auto-PR push credentials. Step-1
  split into 1a/1b. (planner)
