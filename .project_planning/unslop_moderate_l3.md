# Unslop L3 — Oversized-file splits, all packages (das-plan hand-off)

> **Feed this file to `/das-plan`.** It is a scoped, **pure-mechanical** brief: break up
> the oversized/over-responsible Go files across Steiner into cohesive smaller files with
> **zero logic change**. Derived from a 5-slice audit on 2026-06-19. Nothing here is
> implemented. This is **Loop 3 of 4** in the "unslop" program: L1 = non-TUI semantic
> dedup, L2 = TUI semantic dedup, **L3 = this (file splits)**, L4 = aggressive backlog.
>
> **Run order:** L3 runs **after L1 and L2** so the splits rebase on top of all semantic
> edits (mixing moves with edits is exactly what makes diffs unreadable). L3 is
> independent of L4 but ideally precedes it.

## Scope & character

In: **moving code between files within the same package** to bring oversized files under
the CLAUDE.md size targets (~300 LOC, split by ~500) and to separate mixed concerns. Every
change is a cut/paste relocation — **no function bodies change, no symbols are renamed,
no signatures change**. All symbols stay in the same package, so there are zero call-site
or import changes.

Out: any dedup, dead-code removal, or behavior change (those are L1/L2/L4). If a split
*tempts* a logical change, defer that change — keep this loop purely structural so the
reviewer can fast-scan thousands of moved lines with confidence.

## Constraints

- **Pure moves.** `git diff` should show deletions in one file exactly matched by
  additions in another. Reviewer technique: confirm no net logic delta.
- File names: snake_case, descriptive of the concern; **no `util`/`helper`/`common`/
  `support`/`misc`** file names (the project forbids the helper-file anti-pattern).
- After every split: `gofmt -w` + `goimports -w`, `go build ./...`,
  `go test ./internal/<pkg>/...`. Loop end: `make check`.
- Behavior-preserving guarantee: existing tests pass unchanged. Test-file splits (C-017)
  are allowed and are also pure moves.

## Files to split (full detail)

**A-006 — `internal/tui/content_render_chrome.go` (842)** · risk low
Four concerns: (1) approval-pill chrome (`renderApprovalPillSegment`,
`renderApprovalPill`), (2) compaction banner (`renderCompactionBanner`,
`compactionBoxRows`, `compactionDetailRows`, `renderCompactionHeader*`), (3) delegation box
(`renderDelegationSegment`, `renderDelegationBoxRows`, `renderDelegationHeader*`,
`renderDelegationTranscript`, `renderDelegationToolEntry`, …), (4) separators/helpers
(`renderCenteredDashes`, `renderSeparatorSegment`, `formatElapsed`, `nanoNow`,
`delegationStyles`). **Split:** `content_render_approval.go` (~100), `content_render_
compaction.go` (~130), `content_render_delegation.go` (~520), with shared helpers
(`formatElapsed`/`nanoNow`) co-located in the delegation file or a `content_render_
chrome.go` remnant. *(If L2 A-005/A-013 ran first, the helpers `renderStyledBox` etc. move
with their concern.)*

**A-007 — `internal/tui/model_update_keys.go` (751)** · risk low
≥5 concerns: overlay routing (`handleOverlayKeyMsg`), conversation/navigation/composer key
handling, approval keys (`handleApprovalKey`), context-overlay key handling, all
overlay-specific handlers (model picker, workflow handoff, exit modal, slash overlay, file
picker, session picker), tab completion. **Split:** overlay-specific handlers →
`model_update_keys_overlay.go`; approval key logic → `model_update_keys_approval.go`; main
file keeps the routing switch (`handleKeyMsg`, `handleOverlayKeyMsg`,
`handleNavigationKeyMsg`, `handleConversationKeyMsg`, `handleComposerKeyMsg`). Target main
file ~300.

**A-016 — `internal/tui/model_input.go` (685)** · risk low
Mixes `handleEnter` (command dispatch, has a `//nolint:gocyclo`), approval execution,
interrupt/submit/oneshot/steer actions, `syncInputChrome`, `selectedApprovalDecision`,
`moveApprovalSelection`, tab-completion candidates. **Split:** approval methods
(`executeApprovalDecision`, `selectedApprovalDecision`, `moveApprovalSelection`,
`moveToolGroupApprovalSelection`) → `model_input_approval.go` (~60); steer/interrupt/
oneshot execute actions → `model_input_actions.go` (~100); keep `handleEnter`,
`syncInputChrome`, tab completion in `model_input.go` (~450).

**B-007 — `internal/tool/builtin/mutate.go` (821)** · risk low
`mutate_commit.go` / `mutate_diagnostics.go` already split out. Remaining: planner type,
all `plan*` methods (`planCreate/Write/Replace/LineReplace/DeleteLine/Delete/Move/
InsertBefore/InsertAfter`), `validateFields`, `NewMutateTool` handler. **Split:**
`mutate_planner.go` (planner type + state + `planOperation` dispatcher), `mutate_ops.go`
(all `plan*` methods), keep `mutate.go` for `NewMutateTool` + `allowedFields`. *(If L1
B-002/B-004 ran first, the consolidated `planInsert` moves as one unit.)*

**B-013 — `internal/tool/noise_strip.go` (323)** · risk low
Mixes ANSI/CR/progress text transforms (`stripProgressLines`,
`stripCarriageReturnOverwrites`) with truncation-strategy dispatch
(`tailPriorityStrategy`/`countCapStrategy`/`headStrategy`, `collapseToolLines`,
`ShapeToolOutput`). **Split:** `noise_strip.go` (pure text transforms) + `output_shape.go`
(strategy selection + `ShapeToolOutput` entry point).

**C-011 — `internal/agent/compaction.go` (729)** · risk low
Three concerns: orchestration (`Compact`, `compactConversationForBudget`, ~238-290),
two-stage summarization algorithm (~63-236), state helpers (candidate selection, retention
base, escalation, budget fragility, ~400-700+). **Split:** `compaction_escalation.go`
(~645-729), `compaction_candidate.go` (~400-600), keep algorithm + orchestration in
`compaction.go` (<350). *(If L1 C-004/C-016 ran first, those consolidations move with
their concern.)*

**C-017 — `internal/agent/runner_test.go` (2076)** · risk low (test-only)
**Split:** `runner_model_test.go` (model-call scenarios), `runner_tool_test.go` (tool
execution), `runner_compaction_test.go` (compaction integration). Pure test relocation.

**D-007 — `internal/output/debug.go` (399)** · risk low
`ContextDiagnosticsEvent` struct + 5 constructors + the whole formatting subsystem
(`formatContextDiagnosticsEvent` + ~8 sub-formatters). **Split:** `context_diagnostics_
event.go` (struct + constructors) + `context_diagnostics_format.go` (all `format*`). *(Do
NOT attempt the D-008 typed-sub-event refactor here — that is L4/B3.)*

**D-010 — `internal/interactive/session.go` (649, 35 methods)** · risk low
God-object: action dispatch, conversation state, model switching, persistence
(`saveSession`/`rotateSession`/`loadSession`), fork (`handleForkSession`/
`handleForkSavedSession`), replay (`replaySessionMessages`/`replayAssistantToolCalls`/
`replayToolResult`), accessors. **Split:** replay → `replay.go`; persistence →
`persistence.go`; the `Handle*` dispatch already has a home in `actions.go` (147 LOC, has
room). Each resulting file well under 300. `session_test.go` (2533 lines) covers it.

**D-013 — `internal/output/event_constructors.go` (571) + `event_render.go` (495)** ·
risk low
Both mix the oneshot advisor/phase event group with core runtime events. **Split:** extract
advisor/phase events → `event_advisor.go` (types/constructors for AdvisorStarted/Complete/
BudgetExhausted, PhaseTransition, PhaseIndicator) + `event_render_advisor.go` (the 5 render
functions). Brings both files under ~450. *(If L1 D-001/D-002/D-003/D-017 ran first, the
consolidated helpers already shrank these files — re-measure before splitting; the split
may become unnecessary for `event_render.go`. Re-check at plan time.)*

**E-020 — `cmd/steiner/runtime_build.go` (331)** · risk low
FS helpers (`ensureSteinerProjectDir`), config load (`loadRuntimeConfig`), 70-line
assembly (`buildRuntimeWithRoots`), provider factory (`buildRuntimeProviderFactory`), HTTP
client, event sinks, delegation/history/session store builders, skill discovery, sandbox.
**Split:** `runtime_infra.go` (HTTP client, event sinks, loggers — I/O builders) +
`runtime_build.go` (assembly proper: registry, sandbox, session stores). *(L1 E-009 may
have already moved `ensureSteinerProjectDir` out; re-measure.)*

## Important sequencing note

Several L1 items shrink files that also appear here (event_render.go via D-003,
runtime_build.go via E-009, compaction.go via C-004/16, mutate.go via B-002/4). **At plan
time, re-measure each file** after L1 lands — some may drop under target and not need
splitting at all. Only split files still over ~500 LOC.

## Recommended step decomposition for the planner

One step per file (or per package's files), each a self-contained pure-move:
1. TUI splits: A-006, A-007, A-016 (largest churn; possibly one step each).
2. tool splits: B-007, B-013.
3. agent splits: C-011, C-017.
4. output splits: D-007, D-013 (re-measure first).
5. interactive split: D-010.
6. cmd split: E-020 (re-measure first).

All steps are independent (different packages) but keep serial; each verified by its
package's tests + a `git diff` no-logic-delta check.

## Provenance

Audit slices A–E (read-only sub-agents, 2026-06-19). Companion:
`.project_planning/2026-06-19_complexity-reduction/research.md`. Impact: navigability /
CLAUDE.md compliance; ~0 net LOC, ~0 behavior change.
