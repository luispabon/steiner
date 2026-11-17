# Fix history, diagnostics, closeout recovery, and usage forecasts

## Request

Address issues #1054, #1051, #1023, and #1046. All four were labeled `in-progress` at the start of planning.

## Overview

Record interactive oneshot tasks in prompt history. Coordinate diagnostics writers and maintenance with stable per-stream locks and recover interrupted rotation. Persist finalization progress before external closeout effects, and require manual resolution where outcomes are ambiguous. Base window forecasts on elapsed window usage when the window duration and reset time are valid, rather than assuming the first observed sample represents the window start.

## Key Decisions

- D1: Record `/oneshot ` plus the trimmed task through the existing interactive history action only for a new TUI oneshot launch, not a resume or headless invocation. Recalling the entry restores a relaunchable command.
- D2: Serialize all diagnostics mutations per stream with a stable sibling kernel lock, including startup retention, reopen, rotation and append. Add Windows locking, and skip protected mutations with a warning when locking is unsupported or fails. Recover incomplete rotation under the same lock; do not rely on a multi-rename sequence being atomic.
- D3: Persist a versioned report-pending marker when creating a new run, then persist report completion and a closeout intent before calling external closeout. Treat any interrupted intent or closeout error as outcome unknown and never automatically retry it, including legacy `failed` states. Provide explicit manual resolution after checking the provider. Preserve old manifest decoding and phase states.
- D4: For the Claude subscription five-hour window only, estimate average usage from an inferred zero at `ResetsAt - 5h` to the current sample. This is an assumption, not an observation. Keep the history-based calculation for other windows and unusable starts, return no forecast after reset, and do not change budget forecasts.
- D5: Independently implement the four fixes, with diagnostics locking completed before diagnostics coordination, and run integrated review and checks after all changes.

## Tradeoffs

- D2 favors correctness over recording diagnostics when locks are unavailable. Locking each stream separately bounds contention; network filesystem semantics and power-loss durability cannot be universally guaranteed.
- D3 favors preventing duplicate external requests over automatic recovery, including on errors that might have happened before creation. Manual resolution requires the user to inspect the provider.
- D4 uses the window average, not a recent-activity forecast. This can understate a recent spike but avoids implying that a near-zero increase since the first stored reading is representative of usage across the whole window.

## Scope Boundaries

In scope are the four linked issues, adjacent tests, and matching docs. Do not alter headless history, budget forecasting, unrelated stores, provider APIs, or phase status semantics except where required for compatibility. Do not promise cross-process safety for noncooperating writers or filesystems without reliable locking. Power-loss durability is not asserted.

## Verification Strategy

- Cheap: `gofmt -w <changed Go files>` and `goimports -w <changed Go files>` are safe scoped fix modes; `make fmt-check` and `make imports-check` verify. Avoid repo-wide `make fmt`.
- Medium: focused `go test ./internal/<package> -run <test>` then affected package tests; `go build ./...` and `make vet` check compilation and static analysis. No fix mode.
- Expensive: `go test ./...`, `make test-race`, `make lint`, `make build-binaries`, and mandatory `make check` (tidy, format, imports, build, race, vet, lint, vulnerability scan). `make check` needs check tools and `make tidy-check` runs `go mod tidy`, which may alter module files; inspect and report unexpected changes. No broad automatic fix mode.
- Cross-build release targets for locking, including Windows and Darwin, and run actual subprocess lock and diagnostics tests on Linux, macOS, and Windows where runners are available. Cross-build alone does not prove runtime behavior; record unavailable platforms and exact errors.

## Decision Log

- Research required: cross-process lock, rename, and crash behavior is security- and platform-sensitive. External sources and implications are recorded in `research.md`.
- Issue #1046 provides no historical samples, so the exact cause is unconfirmed. The screenshot was viewed: it shows 27% used, 4h21m remaining and the incorrect trend. The issue text's 27% in 40 minutes implies about 108 minutes to exhaustion. Anthropic describes a normal first-use-anchored five-hour session, but does not establish an observed zero at the inferred start; see research.md.
- Legacy completed manifests with no report or closeout marker are conservatively treated as needing report finalization and manual closeout resolution, since they cannot prove an external action did not occur.
- Advisor sanity check: applied all findings: versioned early finalization state and explicit locked resolution; disk metadata refresh, bounded lock wait and crash-safe rotation details; provider-specific projection and stale-window rejection; relaunchable history recall. Viewed the image using its absolute path.
