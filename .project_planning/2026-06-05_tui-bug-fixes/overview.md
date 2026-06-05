# Overview: TUI Bug Fixes (#124, #125, #126)

## Request

Fix three TUI bugs from GitHub issues:

- **#126** — Can't continue conversation after cancelling a delegate (or any tool) call
- **#125** — Various layout problems: broken approval box, spurious approval on already-run bash call, sidebar contents pushed up
- **#124** — Delegated boxes too tall when uncollapsed; extend beyond viewport

## Overview

### #126 — Can't continue after cancel

When a tool or delegate call is cancelled (user hits Ctrl+C / interrupt), the `m.approval` state may remain `active` if the approval tray was open at the time of interrupt. The `ApprovalAccepted`/`ApprovalDenied` events are never emitted on cancel — only `RunFinished` clears `interruptPending`, but nothing clears `m.approval`. The approval tray then stays rendered, the input state is confused, and the user cannot continue.

Fix: in `model_events.go`, clear `m.approval = approvalState{}` on `RunFinishedEvent` and `StopReasonEvent` (already handled for `RunStartedEvent` indirectly — but not on finish/stop).

### #125 — Various layout problems

Three sub-issues visible in the screenshot:

1. **Broken approval box** — The approval tray renders with `contentWidth` (outer column width) but `innerWidth` inside `renderApprovalTray` uses `width-4` assuming border+padding of 4. If `contentWidth` includes padding that the tray doesn't account for, the tray overflows or wraps badly. Cross-check rendered width against actual tray width budget.

2. **Spurious second approval on bash `pwd`** — When an approval is accepted (user clicks Allow), the `ApprovalAccepted` event clears `m.approval`. But if a *second* tool call occurs in the same turn that also requires approval, and the content buffer still has the prior `segmentApprovalPill`, the sequence can produce a visual duplicate. Likely cause: the `contentBuffer.AppendEvent` runs before the model switch clears `m.approval`, so both an in-stream pill AND a fresh tray appear simultaneously for the same pending approval.

3. **Sidebar pushed up** — `sidebar.View(m.width, m.height)` passes `m.width` (full terminal width) as the first arg to `View`, but `View` ignores it and uses the fixed `sidebarWidth`. The height is `m.height` (correct). The push-up is likely caused by the main column overflowing `m.height` when the approval tray or activity row height is under-counted in `layout()`, causing lipgloss to extend the main column and push adjacent sidebar content up via `JoinHorizontal`.

Fix: ensure `layout()` height accounting is exact. Check that `approvalTrayHeight` returns the same value as `lipgloss.Height(renderApprovalTray(...))` at the same width. Add a guard or reconcile the two calls so they cannot diverge.

### #124 — Delegated boxes too tall

`delegationRows()` in `delegation_layout.go` builds all rows unconditionally. The transcript and prompt body rows can be arbitrarily long. When uncollapsed, the delegation box height is unbounded.

The `contentBuffer` doesn't know the viewport height. However, the issue hint says the boxes overshoot by approximately the footer height. The fix is to propagate a `maxLines` cap into `renderDelegationTranscript` and `renderDelegationPromptBody`. The cap should be `viewport.Height` (or slightly less to leave room for border/header/stats rows). The viewport height is available on the `Model`; it needs to pass through to `contentBuffer.String(width)` or via a separate setter.

Simplest approach: add a `maxDelegationBodyLines int` field to `contentBuffer`, set from the model on each layout, and respect it in `renderDelegationTranscript`.

## Verification Strategy

| Command | Cost | Notes |
|---------|------|-------|
| `go test ./internal/tui/...` | medium | primary suite for TUI changes |
| `go test ./...` | medium | full suite |
| `go vet ./...` | cheap | static checks |
| `gofmt -w <files>` | cheap | run after every edit |
| `make check` | medium | repo-mandated pre-finalise gate |

Run targeted `go test ./internal/tui/...` after each step; `make check` before finalising.

## Decision Log

- No external research needed; bugs are fully understood from code inspection and screenshots.
- Three bugs are independent; implement as separate steps that can be reviewed individually.
- `maxDelegationBodyLines` approach chosen over passing height through `String()` to avoid changing the `String` signature used across the codebase.
