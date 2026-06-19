# Oneshot Issue Batch — Overview

## Request

Fetch all open GitHub issues related to the oneshot feature (luispabon/steiner) and
batch them into a single fix plan. The merged PR must auto-close all bundled issues.

## Overview

Six open issues touch the oneshot feature across three subsystems: the interactive
TUI (composer, picker, status output), the oneshot prompt set (plan/implement phase
markdown), and the oneshot engine (resume/boundary logic). This bundle fixes all six
on one loop branch and ships a single PR that closes them on merge.

Bundled issues:

- **#239** (bug/small, TUI): `/oneshot` invoke leaves the typed prompt text in the
  composer; it should clear like other dispatched commands.
- **#240** (big, plan prompt): the plan phase ignored the advisor's findings (no tool
  calls before advancing). Fixed by prompt hardening only.
- **#241** (enh/small, TUI/output): phase-transition status messages are low-visibility;
  the inter-phase separator needs margins and a blank line above and below.
- **#242** (bug/medium, implement prompt): the implement phase drove `mutate` itself
  instead of delegating. Fixed by prompt hardening only.
- **#243** (enh/medium, TUI): resume requires the non-obvious `/oneshot --resume ID`;
  add a discoverable `/oneshot-resume` command backed by a session picker.
- **#244** (bug, oneshot engine): resume breaks when the implement phase fails before
  `execution.md` is written, because the boundary/resume path expects that artifact.

Excluded: **#238** (command palette) — handled separately; only tangentially oneshot.

## Key Decisions

- **#240 and #242 are prompt-only fixes.** No engine guardrails or mechanical gates.
  Behavioral drift is addressed by tightening the plan and implement phase prompts.
  Rationale: "applied advisor findings" and "should have delegated" cannot be detected
  reliably enough to gate on without false positives; the user chose prompt hardening.
- **Preserve existing exemptions when hardening #242.** `internal/oneshot/prompts/implement.md`
  already carries the executor-owned-artifact carve-out (execution.md, branch ops,
  worktree provisioning, `no_delegate` steps) at lines 32–40, mirroring
  `skills/implement/SKILL.md` line 120. A prior over-restriction in skills/implement
  forced the model to delegate even planning-doc updates; the #242 edit must NOT
  reintroduce that. The fix closes the specific rationalization loophole the model
  admitted to ("low ambiguity / small chunks / cheap-feeling mutate = license to skip")
  without making the restriction absolute over executor-owned files.
- **#243 reuses the existing picker pattern.** `session_picker.go`, `plan_picker.go`,
  and `file_picker.go` establish the convention; `/oneshot-resume` opens a session
  picker over resumable runs. The `--resume ID` flag keeps working for scripts.
- **#244 makes resume tolerant, not stricter.** When re-running an incomplete implement
  phase, the engine must not treat a missing `execution.md` as a hard failure — the
  artifact is produced by the implement model near phase end, so a mid-phase failure
  legitimately leaves it absent. The boundary requirement applies only to a *completed*
  phase, not to one being resumed/re-run.
- **PR closes all six.** The closeout step authors a PR body containing
  `Closes #239`, `Closes #240`, `Closes #241`, `Closes #242`, `Closes #243`,
  `Closes #244` so merge auto-closes them.

## Tradeoffs

- **Prompt-only vs guardrails for #240/#242.** Guardrails (a `delegate`-call detector
  surfaced as a report warning) were considered and rejected by the user in favor of
  lower-risk prompt hardening. Cost: regressions are caught only by human review, not
  by an automatic report signal.
- **Single batched PR vs per-issue PRs.** Batching trades reviewability of small PRs
  for one coherent oneshot-hardening changeset and a single closeout. Chosen because
  the issues are thematically tight and share the oneshot subsystem.
- **#244 tolerant-resume vs eager-write.** An alternative is to have the engine write a
  stub `execution.md` at implement-phase start so the artifact always exists. Rejected
  as the primary approach because it pollutes the planning folder on every run and the
  implement model owns that artifact; tolerant resume is the smaller, truer fix. (To be
  confirmed against the exact `resume.go` failure mechanism during step planning.)

## Scope Boundaries

In scope:

- Clear composer text on `/oneshot` dispatch (#239).
- Plan-phase prompt hardening for advisor-finding application (#240).
- Phase status message visibility + separator spacing (#241).
- Implement-phase prompt hardening for delegation, preserving exemptions (#242).
- `/oneshot-resume` command + session picker, `--resume` flag retained (#243).
- Resume tolerance for missing `execution.md` mid-implement (#244).
- Tests for each behavioral/engine change; docs updated per CLAUDE.md maintenance rules
  (ONESHOT.md for engine/prompt changes; README/CONFIGURATION if any config field changes).
- A PR that closes all six issues.

Out of scope:

- #238 command palette work.
- Any engine guardrail / mechanical gate for delegation or advisor application.
- Reworking the advisor subsystem or delegation contracts themselves.
- New config fields (none anticipated).

## Verification Strategy

Source: CLAUDE.md + Makefile (`make check` =
`tidy-check fmt-check imports-check build-binaries test test-race vet lint vuln`).

- **Formatter (cheap, fix mode preferred):** `gofmt -w <files>`, `goimports -w <files>`.
- **Targeted tests (cheap):** `go test ./internal/oneshot -run <Name>`,
  `go test ./internal/tui -run <Name>`.
- **Package tests (medium):** `go test ./internal/oneshot/... ./internal/tui/...`.
- **Build (medium):** `go build ./...`.
- **Vet/lint (medium):** `go vet ./...`, `golangci-lint run ./...`
  (run `golangci-lint cache clean` first to avoid stale-cache false positives).
- **Full gate (expensive, before finalizing):** `make check`.

Prompt-only edits (#240, #242) and pure markdown carry no Go test, but must still pass
`make check` (no Go impact) and be reviewed against the preserve-exemptions decision.
Known environment caveat: `internal/sandbox` bwrap/namespace tests may fail locally due
to nested-userns restrictions — treat such failures as pre-existing, unrelated to this work.

## Decision Log

- 2026-06-19: Excluded #238 (command palette) from the batch per user.
- 2026-06-19: Chose prompt-only fixes for #240 and #242 (no engine guardrails) per user.
- 2026-06-19: Constraint added — #242/#240 prompt edits must preserve executor-owned and
  advisor exemptions to avoid repeating the skills/implement over-restriction regression.
- 2026-06-19: Research deemed not needed (all repo-local); user approved skipping.
- 2026-06-19: PR must use `Closes #NNN` for all six issues per user.
