# Overview — Simple Issues Batch (2026-06-16)

## Request

Fetch open GitHub issues for `steiner`, pick 5 simple ones, and produce one
implementation plan covering all of them. The resulting pull request must
reference the issues it closes.

Selected issues (confirmed with user):

- **#197** — Handoff tool UX improvements (modal text/styling).
- **#190** — `follow_up` tool fixes (render as sub-agent box, colour match, header, guidance).
- **#188** — `workflow_handoff` tool is too specific to plan/implement/review; make generic.
- **#101 + #98** (merged) — Context overlay: open via a keybind instead of the
  `/context` command, show the context **report at the top** and the full
  **context contents below** in one scrollable modal, report rendered as a table.

User amendments to original issues:
- #101: remove `/context` as a slash command; add a keybind toggle modelled on
  `ctrl+b` (sidebar toggle). Merge #98 and #101 into a single modal.
- Process: the final PR must explicitly close all five issues.

## Overview

Five small, repo-local changes grouped into four implementation steps plus a
closeout. No external dependencies or research. Three clusters:

1. **Handoff/sub-agent UX** (#197, #190, #188) — TUI rendering, tool guidance,
   and tool genericisation around the workflow-handoff / delegation surface.
2. **Context modal** (#98 + #101) — replace `/context` command with a keybind,
   merge the report and full-context views into one modal, table-format the report.
3. **Closeout** — single PR that closes #197, #190, #188, #101, #98.

## Key Decisions

- **One PR, five issues.** All five ship on `cl/2026-06-16_simple_issues_batch`
  and the PR body uses `Closes #197`, `Closes #190`, `Closes #188`, `Closes #101`,
  `Closes #98` so GitHub auto-closes them on merge. (User requirement.)
- **#98 and #101 are one feature**, not two. A single modal: context report
  (table) at top, full raw context contents below, scrollable. The existing
  `contextOverlay` (`internal/tui/context_overlay.go`) is the foundation —
  extend it rather than build new.
- **Remove `/context` slash command; add a keybind.** The command wiring
  (`internal/tui/model_input.go` slash item + `interactive.RequestContext`) is
  replaced by a `ctrl+`-style binding in `handleNavigationKeyMsg`
  (`internal/tui/model_update_keys.go`), mirroring `ctrl+b`. Help text
  (`internal/tui/help.go:62`) moves from the slash-command list to the keybind list.
  Chosen key: **`ctrl+t`** (free; `b/p/x/v/a/e/k/w/u` are taken). Confirmed by user.
- **#188 genericisation keeps validators per-target.** `workflow_handoff`
  (`internal/tool/builtin/workflow_handoff.go`) currently hardcodes the
  `implement`/`review` enum, the `.steiner/plans` prefix, and the
  `overview.md`+`plan.yaml` artifact check. Generalise the tool so the
  loop-specific knowledge lives behind a small per-target validator/registry,
  while keeping the existing plan-loop validator as one registered target.
  Tool description and guidance reworded to be workflow-agnostic.
- **#190 reuses delegation rendering.** `follow_up` currently has no event/render
  path and shows as the default JSON tool box. Route it through the existing
  delegation box renderer (`content_events_delegation.go` /
  `content_render_chrome.go`), matching the colour/type of the original child by
  agent ID, and revise the tool guidance so it is actually used.
- **#197 is pure presentation.** Text + bold styling changes in the workflow
  handoff modal (`internal/tui/workflow_handoff_modal.go`). No behaviour change.

## Tradeoffs

- **Merge vs. separate steps for #98/#101.** Merged into one step because they
  touch the same `contextOverlay` state and would otherwise conflict. Rejected
  doing them as two steps.
- **#188 full plugin registry vs. minimal indirection.** A full plugin system is
  over-engineering for one extra target. Chosen: minimal per-target validator
  seam that removes plan-loop assumptions from the core tool while staying small.
  Rejected: leaving hardcoded strings (fails the issue) and a heavyweight registry.
- **Keybind choice.** `ctrl+t` picked for being free and mnemonic-ish ("context").
  Could collide with terminal expectations; surfaced as an open decision.
- **Sequencing.** #197, #190, #188, and the context-modal step are largely
  independent and could be parallelised, but they are kept serial — the
  coordination cost outweighs the benefit for changes this small, and a few touch
  adjacent TUI files.

## Scope Boundaries

In scope:
- The four feature changes above and their nearby unit/functional tests.
- Doc updates mandated by `CLAUDE.md` (tool changes → README/docs; keybind →
  README/help; `/context` removal reflected in docs).
- A single PR closing all five issues.

Out of scope:
- Issues not selected (#203, #198, #192, #173, #144, #104, #99, #96).
- Any redesign of the delegation engine, context-manager internals, or the
  compaction pipeline beyond what the rendering/keybind changes require.
- New config fields unless a change strictly requires one (none anticipated).
- Sandboxing, execution modes, or other larger features referenced by skipped issues.

## Verification Strategy

Discovered from `CLAUDE.md` and the Makefile conventions:

- **Format (cheap, fix-mode):** `gofmt -w <files>`, `goimports -w <files>` after every Go edit.
- **Targeted tests (cheap):** `go test ./internal/tui/... -run <Name>`,
  `go test ./internal/tool/builtin/ -run TestWorkflowHandoff`,
  `go test ./internal/interactive/ -run <Name>` — run first, per step.
- **Build/vet (medium):** `go build ./...`, `go vet ./...`.
- **Full gate (medium/expensive, before finalizing):** `make check`
  (wraps fmt/vet/lint/test). Required before the PR per `CLAUDE.md`.
- **Race (optional, when concurrency touched):** `go test -race ./...` — not
  expected to be needed for these UI/tool changes.

Each step lists targeted tests; the closeout runs `make check`.

## Decision Log

- 2026-06-16: User selected issue set #197, #190, #188, #101, #98 (declined the
  #104/#198 swap).
- 2026-06-16: User merged #98 and #101 into one context modal; required removing
  `/context` command in favour of a keybind.
- 2026-06-16: User required the PR to reference/close all five issues.
- 2026-06-16: Research decision — none needed (repo-local, stable). User confirmed.
- 2026-06-16: Branch based off clean `main` (user switched away from the unmerged
  cave_human WIP branch); planning branch `cl/2026-06-16_simple_issues_batch`.
- 2026-06-16: `.project_planning/` is git-tracked → planning artifacts are committed.
- 2026-06-16: User confirmed keybind `ctrl+t` for the context modal.
- 2026-06-16: User chose #188 Option 1 (minimal per-target validator seam) over a full registry.
