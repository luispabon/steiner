## Request

GitHub issue #109: Thinking block improvements. Two problems:

1. Thinking block text does not wrap at viewport width — long lines extend past the visible area.
2. Collapsed thinking blocks show too little text (~60 chars). Should show a few lines before truncation.

## Overview

Both bugs are in `internal/tui/content_render_markdown.go` `renderThinkingBlockSegment()`.

**Bug 1 — No wrapping:** The expanded body renders each line with `style.Render(line)` but never word-wraps to the available width. The delegation thinking renderer (`renderDelegationThinkingEntry`) already solves this using `wrapStyledDelegationLines` which applies `ansi.Wordwrap` + `ansi.Hardwrap`. Fix: pass `width` through to `renderThinkingBlockSegment` and wrap body lines the same way.

**Bug 2 — Collapsed preview too short:** The collapsed state shows a single line with 60 chars from `td.preview`. Fix: at render time, word-wrap the body to the available width and show the first 3 lines, appending "…" if truncated. This makes `td.preview` field unnecessary for rendering (body is the source of truth). The 80-char `preview` field in `thinkingBlockData` can remain for streaming display or be simplified.

### Key files

- `internal/tui/content_render_markdown.go` — `renderThinkingBlockSegment` (primary fix)
- `internal/tui/content_render.go` — `renderSupplementalSegment` (pass width through)
- `internal/tui/content_events.go` — `thinkingBlockData` struct (preview field, no struct changes needed)

### What doesn't change

- `thinkingBlockData` struct stays as-is
- Preview population in `content_events_stream.go` stays (used during streaming)
- `renderThinkingSegment` (legacy segment type) not affected
- Delegation thinking rendering not affected

## Verification Strategy

| Check         | Command               | Cost   |
|---------------|-----------------------|--------|
| Format        | `gofmt -w <files>`    | cheap  |
| Imports       | `goimports -w <files>`| cheap  |
| Build         | `go build ./...`      | cheap  |
| Vet           | `go vet ./...`        | cheap  |
| Test          | `go test ./internal/tui/...` | medium |
| Lint          | `golangci-lint run ./...` | medium |
| Full check    | `make check`          | expensive |

Run targeted tests first, `make check` at end.

## Decision Log

- Wrap using `ansi.Wordwrap` + `ansi.Hardwrap` (same as delegation thinking) — proven pattern in codebase.
- Collapsed state: derive from body at render time (3 wrapped lines), not from stored preview field. Width-aware = looks correct at any terminal size.
- Keep `td.preview` field for now — used during streaming phase. No struct changes.
