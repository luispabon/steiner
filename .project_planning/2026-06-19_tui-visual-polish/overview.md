## Request

Batch fix for 4 TUI visual issues: #233, #236, #212, #231.

## Overview

Four bounded rendering changes in `internal/tui`, all improving visual clarity and correctness:

1. **#233 — Mutate tool box shows ✅ even with failed operations.** The `applyFinishedToolCallResult` function sets `meta = "✅"` unless `payload.Error != ""`. But mutate can succeed at the transport level (no error string) while having partial operation failures (`operations_failed > 0`). The preview already parses this into `preview.HunksFailed`. Fix: after building the preview, check `td.tool == "mutate" && td.preview.HunksFailed > 0` and flip to `hasError = true` / `meta = "❌"`.

2. **#236 — Delegation box prompt and transcript look identical.** Both prompt body and assistant transcript entries render with `FgMute` style, no visual boundary. Fix: (a) render prompt body lines in italic, (b) add a thin separator line between the prompt section and transcript section in `delegationRows`.

3. **#212 — Bold text in overlays/dialogs should use accent colour.** Three locations use `Bold(true)` with plain white foreground: exit modal title (`exit_modal.go:50-53`), context overlay title (`context_overlay.go:101-104`), and help overlay headings (`help.go:80-82`). Fix: change all three to use `AccentColor` as the foreground alongside bold.

4. **#231 — Add timestamps to conversations.** Users juggling multiple sessions can't tell when things happened. Fix: add a wall-clock timestamp to (a) user message segments (right-aligned in the top padding row) and (b) assistant turn completion (appended as a muted timestamp after stop reason). Timestamps stored as `time.Time` on the segment at creation time, formatted as `15:04` (24h clock, today) or `Jan 02 15:04` (older).

## Key Decisions

- **#233**: Check `preview.HunksFailed` rather than parsing the result JSON again — the data is already available on the preview struct after `BuildToolPreview`.
- **#236**: Italic for prompt, not a different colour — italic is a distinct typographic signal without adding another colour to an already colourful delegation box.
- **#212**: Apply accent to all three overlay heading locations. Slash overlay already uses accent. Pickers (file, session, plan, oneshot) don't have standalone bold headings — they use the PaletteOverlay frame.
- **#231**: Timestamps on user segments and stop-reason completion only — not on every tool call or assistant chunk. This balances orientation vs visual noise. Format is `15:04` for today, `Jan 02 15:04` for older. Stored as `time.Time` on `contentSegment` to allow re-rendering when the day rolls over.

## Tradeoffs

- **#231 relative vs absolute timestamps**: Could show "2m ago" instead of "15:04". Rejected — relative timestamps need constant re-rendering on tick and become confusing at scale ("47m ago" vs "14:22").
- **#236 separator style**: Could use a full-width dashed line. Chose a thin muted `─` line — consistent with existing delegation footer separators.
- **#212 scope**: Could also accent-colour bold text inside glamour-rendered markdown. Out of scope — the issue specifically targets dialogs/overlays, and markdown bold serves a different purpose (emphasis within prose).

## Scope Boundaries

**In scope:**
- `internal/tui/content_events_tool_state.go` — mutate error detection
- `internal/tui/content_render_chrome.go` — delegation prompt/transcript rendering
- `internal/tui/delegation_layout.go` — separator between prompt and transcript
- `internal/tui/content_events.go` — contentSegment timestamp field
- `internal/tui/content_render_markdown.go` — user segment timestamp rendering
- `internal/tui/content_events_tool_state.go` — stop reason timestamp
- `internal/tui/exit_modal.go`, `context_overlay.go`, `help.go` — accent bold
- Tests for all changed rendering logic

**Out of scope:**
- No changes to `internal/tool`, `internal/agent`, `internal/output`, `internal/prompt`
- No config fields
- No doc updates (none of the maintenance rules are triggered)
- No changes to glamour markdown rendering
- Sidebar, statusbar, and palette are not affected

## Verification Strategy

Repository verification commands (from CLAUDE.md):

| Command | Cost | Notes |
|---------|------|-------|
| `gofmt -w <files>` | cheap | Run after edits |
| `goimports -w <files>` | cheap | Run after edits |
| `go build ./...` | cheap | Compile check |
| `go vet ./...` | cheap | Static analysis |
| `go test ./internal/tui/ -run <specific>` | cheap | Targeted tests |
| `go test ./...` | medium | Full suite |
| `go test -race ./...` | medium | Race detector |
| `make check` | medium | Full verification (mandated before finalizing) |

## Decision Log

| Decision | Rationale |
|----------|-----------|
| Batch 4 issues together | All touch `internal/tui` rendering, no cross-cutting concerns |
| Use `preview.HunksFailed` for #233 | Data already parsed and available, no redundant JSON parsing |
| Italic for delegation prompt (#236) | Distinct from transcript without adding colours |
| `time.Time` field on contentSegment (#231) | Allows format to change based on age; avoids storing pre-formatted strings |
| 24h clock format (#231) | Unambiguous, compact, consistent with elapsed time displays already in the TUI |
