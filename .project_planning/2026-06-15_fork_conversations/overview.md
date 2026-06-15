## Request

Allow forking conversations into new sessions, keeping full context and model for maximum prompt cache hits. Support forking from both the current live session and saved sessions in the session picker. UX should feel familiar to users of other AI tools. (GitHub issue #183)

## Overview

Add a `/fork` slash command and a session picker fork action. Forking deep-copies the source session's conversation lineage and model into a new session with a new ID, saves the original (if live), and switches to the fork. The forked session title indicates its origin (e.g. "Fork of: <parent title>").

The implementation touches three layers:
1. **Session store** (`internal/session`): add a `Fork` method that clones lineage + model into a new session
2. **Interactive session** (`internal/interactive`): add `ForkSession` and `ForkSavedSession` actions, wire into `handleStateAction`
3. **TUI** (`internal/tui`): add `/fork` slash command, add fork option to session picker

## Key Decisions

- **Fork = deep copy, not reference**: forked session is fully independent. No parent-child linkage tracked in the session store. Keeps the model simple and avoids orphan/dangling-reference problems.
- **Auto-switch to fork**: after forking, user lands in the new session. Original is saved. This matches ChatGPT's pattern and is the intuitive behavior ("I want to try something different from here").
- **Title includes origin**: "Fork of: <original title>" makes lineage visible without needing a tree structure. Truncated to 80 chars per existing `TitleFromPrompt` convention.
- **Same model preserved**: forked session uses the same model ID as the source, maximizing prompt cache hits as stated in the issue.
- **Session picker fork**: adds a fork keybinding (e.g. `f`) in the session picker overlay, operating on the highlighted saved session without loading it first.
- **Session picker datetime**: each entry in the session picker shows a leading datetime in `[ YYYY-MM-DD HH:MM:SS ]` format for easier identification.

## Tradeoffs

- **No fork tree/graph**: simpler but loses ability to navigate fork history. Acceptable for v1; the title convention provides enough lineage info. Can be added later if needed.
- **No fork-from-message (ChatGPT style)**: ChatGPT lets you edit a past message and branch from there. This is a different, more complex feature. `/fork` clones the entire conversation as-is. Fork-from-message could be a future extension.
- **No fork limit**: sessions already evict at 25 (maxSessions). Forks count toward this limit. No separate fork quota needed.

## Scope Boundaries

**In scope:**
- `/fork` slash command (forks current live session)
- Fork action from session picker (forks a saved session)
- `ForkSession` / `ForkSavedSession` actions in `internal/interactive`
- `Fork` helper in `internal/session` (deep-copy lineage + new ID)
- Status message confirming fork ("Forked from: <title>")
- Tests for all new code paths

**Out of scope:**
- Fork tree/graph visualization
- Fork-from-message (edit-and-branch)
- Merging forked sessions
- CLI flag for forking (interactive-only)
- Forking sub-agent/delegation sessions

## Verification Strategy

**Verification commands (from Makefile `check` target):**

| Command | Cost | Notes |
|---------|------|-------|
| `gofmt -w <files>` | cheap | auto-fix formatting |
| `goimports -w <files>` | cheap | auto-fix imports |
| `go build ./...` | cheap | compilation check |
| `go test ./internal/session/...` | cheap | targeted package tests |
| `go test ./internal/interactive/...` | medium | interactive session tests |
| `go test ./internal/tui/...` | medium | TUI tests |
| `go test ./...` | medium | full test suite |
| `go test -race ./...` | medium | race detection |
| `go vet ./...` | cheap | static analysis |
| `golangci-lint run ./...` | medium | linting |
| `make check` | expensive | full pipeline (tidy, fmt, imports, build, test, race, vet, lint, vuln) |

**Strategy**: run targeted tests per step, `make check` at the end.

## Decision Log

| Decision | Date | Rationale |
|----------|------|-----------|
| Fork = deep copy, no parent link | 2026-06-15 | Simplicity; avoids orphan references when sessions are evicted |
| Auto-switch to fork | 2026-06-15 | Matches ChatGPT pattern; intuitive "try something different" flow |
| Support both live and saved session forking | 2026-06-15 | User requirement from issue discussion |
| Session picker datetime format `[ YYYY-MM-DD HH:MM:SS ]` | 2026-06-15 | Easier identification of sessions by time |
| Title convention "Fork of: X" | 2026-06-15 | Lightweight lineage visibility without schema changes |
| No fork tree in v1 | 2026-06-15 | Complexity vs. value; title convention sufficient for now |
