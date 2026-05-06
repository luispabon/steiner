# Overview: Session Persistence and Resuming

## Request

Implement session persistence and the ability to resume sessions.

**Acceptance criteria:**
- Conversations are persisted to disk after each completed turn
- User can list previous sessions (`steiner --resume` or `/resume` in TUI)
- User can resume a specific session (`steiner --resume <uuid>`)
- Resumed session restores full conversation history including compaction lineage
- On exit, steiner prints a resume hint with the session UUID
- Max 25 sessions retained; oldest evicted automatically

**UX spec:**
- Sessions identified by UUID
- `steiner --resume <uuid>` — resume specific session
- `steiner --resume` (no arg) — print session list to stdout and exit
- `/resume` in-session slash command — interactive TUI session picker overlay (searchable list, like Claude Code's session picker)
- Session title derived from first user message, truncated

## Overview

### New package: `internal/session`

Single-responsibility package owning session storage. No dependency on TUI or CLI.

**Storage layout** (`~/.config/steiner/sessions/`):
```
sessions/
  index.json          # lightweight metadata list for fast listing
  <uuid>.json         # full session including conversation lineage
```

**Session record** (full file):
```go
type Session struct {
    ID        string                    `json:"id"`
    CreatedAt time.Time                 `json:"created_at"`
    UpdatedAt time.Time                 `json:"updated_at"`
    Title     string                    `json:"title"`   // first user msg, ≤80 chars
    Model     string                    `json:"model"`
    Lineage   agent.ConversationLineage `json:"lineage"` // full compaction history
}
```

**Index entry** (index.json — for listing without loading full files):
```go
type IndexEntry struct {
    ID        string    `json:"id"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
    Title     string    `json:"title"`
    Model     string    `json:"model"`
}
```

Writes use tmp-file + rename (atomic). Index updated on every save. Eviction removes both the session file and the index entry when count > 25. All operations on `Store` are mutex-protected.

### JSON tags on agent lineage types

`agent.ConversationGeneration` and `agent.ConversationLineage` in `internal/agent/state.go` lack JSON tags. `agent.Message` already has them. Adding tags to the lineage types makes `Session.Lineage` serialize correctly with zero custom marshalling. This is a non-breaking additive change.

### Session lifecycle wiring (`internal/interactive`)

`interactive.Session` gains a `SessionStore` dependency. On session start:
- Generate UUID, set title from first prompt, create session record
- After each completed agent run (no error): update session file + index

New interactive action `LoadSession{Lineage agent.ConversationLineage}` — replaces the active conversation with the loaded lineage (mirrors `ClearConversation` pattern but seeds state instead of wiping it).

`interactive.Session` already tracks `conversation []Message`; restoring from lineage follows `RunState.WithLineage(lineage)` → sets both `Conversation` and `Lineage`.

### CLI: `--resume` flag (`cmd/steiner`)

Added to `cliFlags` as a string flag with `cobra` `NoOptDefVal`:
- `--resume` alone → value `""` → list sessions to stdout, exit 0
- `--resume <uuid>` → load session, inject lineage as initial conversation

`buildRuntime` / `runInteractiveMode` handles both cases. Session store path: `filepath.Join(homeDir, ".config", "steiner", "sessions")` (parallel to existing `history.log`).

On clean session exit: print `\nResume this session: steiner --resume <uuid>` to stderr.

### TUI `/resume` overlay (`internal/tui`)

New `sessionPickerOverlay` in `internal/tui/session_picker.go` — embeds `OverlayShell` (same pattern as `filePickerOverlay`). Features:
- Searchable list filtering on title + model + date
- Arrow keys to navigate, Enter to confirm, Esc to cancel
- Shows: title, model, relative date, size hint
- Max display = 8 entries at a time (matches `filePickerOverlay.maxDisplay`)

`/resume` added to `internal/tui/input.go` slash command list and `parseInput` switch. `internal/interactive/actions.go` gains `RequestSessionPicker` action. The TUI model opens the overlay on `RequestSessionPicker`; on selection it dispatches `LoadSession{Lineage}` to the controller.

Session store passed to TUI model via `App` config (alongside existing `Controller`).

### Data flow on resume

```
steiner --resume <uuid>
  → load Session from Store
  → extract Lineage
  → runInteractiveMode with initialLineage
    → interactive.Session.SetLineage(lineage)
      → agent RunRequest seeded with lineage.FullMessages()
```

## Verification Strategy

### Sources
- `Makefile` / `go.mod`
- `CLAUDE.md` work loop
- No CI config discovered in-tree

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: false

### Commands

#### format
- preferred_mode: fix
- fix:
  - `gofmt -w <changed files>`
- check:
  - `gofmt -l <changed files>`
- use_check_only_when:
  - never (fix is always safe)

#### vet
- preferred_mode: check
- fix: none
- check:
  - `go vet ./internal/session/... ./internal/agent/... ./internal/interactive/... ./internal/tui/... ./cmd/steiner/...`
- use_check_only_when: always (no fix mode)

#### build
- preferred_mode: check
- fix: none
- check:
  - `go build ./...`
- use_check_only_when: always

#### unit-tests-targeted
- preferred_mode: check
- fix: none
- check:
  - `go test ./internal/session/...`
  - `go test ./internal/agent/...`
  - `go test ./internal/interactive/...`
  - `go test ./internal/tui/...`
  - `go test ./cmd/steiner/...`
- use_check_only_when: always

#### unit-tests-all
- preferred_mode: check
- fix: none
- check:
  - `go test ./...`
- use_check_only_when: always

### Tiers
- cheap:
  - format
  - vet
  - build
- medium:
  - unit-tests-targeted
- expensive:
  - unit-tests-all

### Required Boundaries
- step_level_exceptions:
  - none
- stage_level_exceptions:
  - none
- end_of_implementation:
  - format
  - vet
  - build
  - unit-tests-all
- reviewer_after_fix:
  - Run targeted tests for the packages touched in the fix
  - Re-run `go build ./...` after any structural change

### Assumptions
- No CI config in-tree; verification based on CLAUDE.md work loop
- Session store path follows existing pattern: `~/.config/steiner/`
- `gofmt -w` on changed files only (not repo-wide) per CLAUDE.md

### Uncertainties
- Whether `interactive.Session` exposes enough hooks to inject initial lineage cleanly without restructuring `run_flow.go`
- Exact cobra `NoOptDefVal` wiring for `--resume` optional-arg pattern

## Decision Log

- **JSON over YAML/SQLite**: stdlib `encoding/json`, zero deps, atomic writes mitigate brittleness. SQLite adds cgo dep, incompatible with steiner's minimal ethos.
- **Max 25 sessions**: user-specified; enforced at write time via index eviction.
- **Serialize `Lineage` not just `Conversation`**: preserves compaction generations, allowing resumed session to continue compaction correctly. Requires adding JSON tags to lineage types (non-breaking).
- **`~/.config/steiner/sessions/`**: follows existing `history.log` path pattern; consistent XDG config dir usage.
- **Index file + per-session files**: index enables fast listing (25 entries) without loading all full JSON blobs; per-session files keep individual saves small and atomic.
- **`NoOptDefVal` for `--resume`**: allows `steiner --resume` (list) and `steiner --resume <uuid>` (resume) with a single flag.
- **TUI overlay over huh form**: session picker is a TUI concern; uses existing `OverlayShell` + `filePickerOverlay` pattern, avoiding huh terminal takeover.
