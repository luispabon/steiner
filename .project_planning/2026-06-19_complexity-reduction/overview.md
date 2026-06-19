# Overview — Unslop L1: non-TUI semantic dedup & dead-code

## Request

Steiner was vibecoded and has accreted repetition, overengineering, and dead code across
~49k LOC. A 5-slice audit produced 93 findings (see `research.md`), now organized into a
4-loop program (L1–L4) captured in `.project_planning/unslop_moderate_l{2,3,4}.md`. **This
loop executes L1**: the behavior-preserving semantic dedup and dead-code removal across
every package **except `internal/tui`** (L2) and excluding all oversized-file splits (L3)
and the aggressive structural backlog (L4).

## Overview

L1 is the largest, safest, highest-yield slice: cross-package helper dedup, the
`internal/output` event boilerplate consolidation (~200 LOC), the `internal/config` patch/
validate collapse (~130 LOC), the `internal/tool/builtin` local dedup (~150 LOC), the
`internal/agent` thin-wrapper/seam collapse, the `internal/provider` wire dedup, a
cross-package dead-code sweep, and small-package (delegation/cmd/advisor/session)
consolidation. ~60 findings, est. **>700 LOC removed**, all behavior-preserving and guarded
by the existing ~69k-LOC test suite.

Organized into **package/concern-scoped steps**, executed **serial**, cross-package
shared-symbol changes first. Per-step verification via targeted package tests; full
`make check` once at the end.

## Key Decisions

- **KD1 — L1 = non-TUI semantic dedup + dead-code, no splits, no re-architecture.** TUI
  semantic dedup is L2, all file-splits are L3, aggressive structural work is L4. Rationale:
  isolate by *character* so each PR reviews in one mode and the risky work is quarantined.
- **KD2 — Shared-symbol changes first.** The cross-package helper dedup (T1: export
  `provider.Clone*` / `output.SupportsANSI`, delete the copies) runs as step 1 so later
  steps build on a deduped base and don't re-introduce drift.
- **KD3 — Behavior-preserving only.** No feature/behavior/public-API change beyond
  compiler-enforced renames. Event-render and wire-serialization consolidations must be
  byte-for-byte verified by existing golden/round-trip tests. **No test edited to
  accommodate a behavior change.**
- **KD4 — Borderline items verified-or-deferred.** Five L1 findings shade toward behavioral
  (C-009 cancel path, C-013 MarshalJSON omitempty, C-019 turn-completion emit ordering,
  D-006 event MarshalJSON omitempty, B-009 double file read). For each: consolidate **only**
  if an existing test pins the current behavior; otherwise leave it and note it for a
  follow-up. E-011 (follow-up double-count) and C-010 are **not** in L1 — they are L4/B9.
- **KD5 — No new util/helper/common packages.** Shared helpers go in the owning package;
  exported symbols justified by cross-package use + Godoc (per CLAUDE.md).
- **KD6 — Delegation file *merges* (E-012/13/14) belong in L1, not L3.** They delete tiny
  helper-named files by inlining single-caller code — that is dedup/anti-pattern removal,
  not a god-file split.

## Tradeoffs

- **One large L1 PR vs many small ones.** Chosen: one L1 loop (user wants ≤4 loops total).
  Mitigation: package/concern-scoped steps keep each *step's* diff reviewable even though
  the loop total is large.
- **Borderline items: attempt vs defer.** Chosen: verify-or-defer (KD4) — safety over
  completeness. Rejected: writing characterization tests for them now (that is L4's mode).
- **`internal/output` split (D-013) deferred to L3** even though L1 dedup touches the same
  files. Rationale: keep semantic edits and pure moves in separate PRs; L3 re-measures and
  may skip the split if L1 already shrank the file.

## Scope Boundaries

**In scope (L1 findings):**
- T1 cross-package dedup: C-005, D-004, E-006
- T2 `output` event boilerplate: D-001, D-002, D-003, D-017 (+ D-006, D-011, D-012)
- `output`/`oneshot` misc: D-014, D-015, D-016, D-018
- T3 `config` patch/validate: E-001, E-002, E-021
- T4 dead code: E-018, E-005, E-007, E-003, E-004, B-014, D-005, B-006, C-020
- T5 `tool/builtin` dedup: B-001, B-002, B-003, B-004, B-005, B-009, B-010, B-011, B-012, B-015
- T6 `agent` thin-wrapper/seam: C-004, C-006, C-007, C-008, C-009, C-015, C-016, C-018, C-019
- T7 `provider` wire dedup: C-002, C-012, C-013, C-014
- T10 small-pkg consolidation + boundary: E-012, E-013, E-014, E-016, E-017, E-019, E-022, E-009, E-010
- Doc updates in-commit only where a touched surface is documented (E-010 → docs/CONFIGURATION.md if a `CLIOverrides` field is added).

**Out of scope:**
- All `internal/tui` semantic dedup → **L2** (`unslop_moderate_l2.md`).
- All oversized-file splits (A-006/7/16, B-007, B-013, C-011, C-017, D-007, D-010, D-013,
  E-020) → **L3** (`unslop_moderate_l3.md`).
- Aggressive backlog B1–B9 (incl. C-010, E-011, A-008, A-011, A-014, B-008, C-001, D-008,
  D-009, E-008-deep) → **L4** (`unslop_moderate_l4.md`).
- Any feature/behavior/perf change; worktree/vendor trees.

## Verification Strategy

From CLAUDE.md + Makefile + go.mod (Go 1.26):

| Stage | Command | Cost | Mode |
|---|---|---|---|
| Format | `gofmt -w <files>` then `goimports -w <files>` | cheap | fix |
| Per-step test | `go test ./internal/<pkg>/...` (targeted, table-driven) | cheap–med | check |
| Build | `go build ./...` | med | check |
| Vet | `go vet ./...` | med | check |
| Lint | `golangci-lint run ./...` | med | check |
| Loop end | `make check` = tidy-check fmt-check imports-check build-binaries test test-race vet lint vuln | expensive | check |

Per-step: `gofmt`/`goimports` (fix) → touched package's targeted tests + `go build ./...`.
Behavior-preservation = existing tests stay green with **no test modified to accommodate a
behavior change** (test edits limited to nothing in L1 — L1 introduces no file splits).
Race + vuln run only at loop end via `make check`.

## Decision Log

- 2026-06-19: Strategy = audit-first, phased. Aggressiveness = moderate.
- 2026-06-19: Audit method = skill-scan (slop-detector + go-code-audit +
  improve-codebase-architecture) + synthesis; delegated to 5 read-only sub-agents.
- 2026-06-19: Audit complete — 93 findings, 10 themes + 9 backlog (research.md).
- 2026-06-19: User chose a **4-loop program** (≤4 cycles). Split: L1 non-TUI semantic dedup
  + dead-code (this loop), L2 TUI dedup, L3 file-splits, L4 aggressive backlog.
- 2026-06-19: L2/L3/L4 persisted as das-plan hand-offs (`unslop_moderate_l{2,3,4}.md`);
  combined `unslop_moderate.md` deleted.
- 2026-06-19: **This loop scoped to L1** (~60 findings). Borderline items verify-or-defer
  (KD4). Shared-symbol dedup first (KD2).
