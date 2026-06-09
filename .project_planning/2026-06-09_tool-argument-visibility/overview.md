## Request

Closes #149. Several built-in tools display opaque headers and/or bodies in the TUI — the user cannot tell what arguments the model passed without expanding or guessing. Each tool needs a purpose-built argument visualisation in both the collapsed header and the expanded body.

## Overview

All changes are in `internal/tui/` (header summaries and body renderers) with no changes to tool execution, schemas, or output event types.

### Header changes (`summarizeArgs` in `content_tool.go`)

| Tool | Current | Proposed |
|------|---------|----------|
| **grep** | Shows `path` (first matching key) | `'pattern' in ./path` or `'pattern' in ./path/*.glob` when glob arg present |
| **glob** | Shows `path` | Combined `./path/**/*.pattern` — path + pattern merged into one natural path |
| **fetch_url** | Random first map value (`url` not in key list) | Show `url` reliably |
| **read** | Shows `path` only | `file.go:start–end` with line range; italicize default values (offset=1, limit=200) |
| **ls** | Shows `path` | Append `(recursive)` when `recursive=true` |
| **mutate** | Shows first op path + count | `op_type path` e.g. `replace file.go`, `move a.go → b.go`, `create file.go (+2 more)` |

### Body changes (`content_render_preview.go`)

| Tool | Change |
|------|--------|
| **mutate insert_before** | New case: `I` badge (green) + `path:line`, diff block with green `+` added lines |
| **mutate insert_after** | Same as insert_before |

### No changes needed

| Tool | Reason |
|------|--------|
| **bash** | Header shows command, body shows `$ command` + output + exit code — already good |
| **web_search** | Header shows `query`, body renders as file kind — already good |
| **display_file** | Filtered out of tool call rendering, has its own overlay |
| **workflow_handoff** | Rendered as modal overlay, not a tool call box |
| **mutate create/write/replace/line_replace/delete/move** | Body rendering already has rich per-operation diffs and badges |

### Key decisions

- grep header: single-quote the pattern to visually separate regex from path (`'func.*Schema' in ./internal`)
- glob header: merge path+pattern into one path string (`./internal/**/*.go`) rather than `**/*.go in ./internal`
- read header: always show line range; italicize values that came from defaults (offset=1 when not sent, limit=200 when not sent) using `lipgloss.Italic()`
- ls header: only annotate `(recursive)` — no other arg changes
- mutate header: show first operation type + path, keep `(+N more)` for multi-op
- mutate insert body: reuse existing `addedBg` diff style, show line number like `line_replace` does

## Verification Strategy

| Command | Cost | Purpose |
|---------|------|---------|
| `gofmt -w <files>` | cheap | Format after edits |
| `goimports -w <files>` | cheap | Fix imports |
| `go build ./...` | cheap | Compile check |
| `go vet ./...` | cheap | Static analysis |
| `go test ./internal/tui/ -run <targeted>` | cheap | Targeted test run |
| `go test ./...` | medium | Full test suite |
| `go test -race ./...` | medium | Race detector |
| `make check` | expensive | Full pipeline: tidy, fmt, imports, build, test, race, vet, lint, vuln |

Strategy: targeted tests after each step, `make check` at the end.

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | grep: quote pattern in header | Visually separates regex from filesystem paths |
| 2 | glob: merge path+pattern | More natural than "pattern in path" — reads like a real glob path |
| 3 | read: always show range, italicize defaults | Shows full context; italic distinguishes explicit vs inferred args |
| 4 | mutate: show op type in header | Just the filename is useless — need to know what was done |
| 5 | insert_before/after: reuse addedBg diff style | Consistent with existing replace/line_replace rendering |
| 6 | No changes to bash, web_search, display_file, workflow_handoff | Already have good argument visibility or use separate rendering paths |
