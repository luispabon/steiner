# Overview: Immediate Tickets T001–T008

## Request

Implement the 6 immediate-priority tickets from `BACKLOG.md`:

- **T001** — Remove "thinking_chunk" from app logs by default
- **T002** — Move system prompt out of code into config
- **T003** — Move other hardcoded model prompts into config
- **T004** — List files in folder/subfolders bypassing the agent
- **T005** — File auto-completion via `@`
- **T006** — Conversation pane scrollbar
- **T008** — Tool path blacklist

## Overview

This is a multi-subsystem change touching `internal/config`, `internal/prompt`, `internal/tool`, and `internal/tui`. The work is divided into 6 stages.

### Stage 1 — Config Shape Change (T002 + T003)

Transform the top-level `Config.Model` from a `string` key into a full `ModelConfig` struct. Add `Prompts` fields (`System`, `Compaction`) to `ModelConfig`. Update all consumers. Keep embedded defaults in `internal/prompt/system.go` and `compaction.go` as fallbacks. Load per-model overrides from config.

### Stage 2 — Thinking Chunk Toggle (T001)

Add `logging.thinking_chunk` bool (default `false`). Gate `EventTypeThinkingChunk` emission in `FileLogSink`.

### Stage 3 — Tool Path Blacklist (T008)

Add `ExcludePaths` and `ExcludePatterns` to `PathsConfig`. Build a `PathExcluder` with user-configurable values plus built-in heuristics (`.git`, `node_modules`, `.steiner`, `vendor`, `.cache`, build dirs). Replace Dive's `GlobTool` with a custom `filepath.WalkDir`-based walker that prunes excluded directories. Replace Dive's `GrepTool` with a custom recursive walker or `rg` wrapper that skips excluded dirs. Bash tool is exempt.

### Stage 4 — Conversation Scrollbar (T006)

Add a custom visual scrollbar to the right edge of the conversation viewport using Lipgloss. The viewport already scrolls; this is purely visual feedback.

### Stage 5 — File Listing Overlay (T004)

Add `/ls [path]` slash command. Open a scrollable modal overlay (reusing the palette overlay pattern) listing files recursively. Default to project root if no path given. Render as a flat list. Close with `Esc` or `Enter`. No agent invocation.

### Stage 6 — `@` File Auto-completion (T005)

When the user types `@` in the textarea, open a fuzzy file picker overlay (files and directories) above the input. Filter candidates as the user types. `Up`/`Down` to navigate, `Enter` to insert the selected path (with trailing space), `Esc` to close. Use project-root-relative paths. Respect T008 exclusions.

## Verification Strategy

### Sources
- `Makefile`
- `AGENTS.md`

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### Format
- preferred_mode: fix
- fix:
  - `gofmt -w <files>`
- check:
  - `gofmt -d $(GO_FILES)`

#### Lint
- preferred_mode: check
- check:
  - `go vet ./...`

#### Unit Tests
- preferred_mode: check
- check:
  - `go test ./internal/config/...`
  - `go test ./internal/tool/...`
  - `go test ./internal/tui/...`

#### Full Test Suite
- preferred_mode: check
- check:
  - `go test ./...`

#### Build
- preferred_mode: check
- check:
  - `go build ./...`
  - `make build-binaries`

### Tiers
- cheap:
  - Format
  - Lint
- medium:
  - Unit Tests
- expensive:
  - Full Test Suite
  - Build

### Required Boundaries
- step_level_exceptions: none
- stage_level_exceptions: Format must run after any Go file edit
- end_of_implementation:
  - Full Test Suite
  - Lint
  - Build
- reviewer_after_fix:
  - Rerun minimal relevant checks first

### Assumptions
- Dive toolkit version is fixed; we won't upgrade it for exclusion support
- `rg` (ripgrep) may not be available in all environments, so custom walkers are needed
- The `bubbles/viewport` scrollbar can be implemented via custom Lipgloss rendering

### Uncertainties
- Whether the custom grep walker will match Dive's GrepTool output format exactly

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | Top-level `Model` becomes full `ModelConfig` | User request; breaks backwards compat intentionally |
| 2 | Embedded defaults preserved in `.go` files | User request; config overrides only when provided |
| 3 | T008 exclusions applied before traversal | User request; prevents looking where we shouldn't |
| 4 | Dive `GlobTool`/`GrepTool` replaced with custom walkers | Dive doesn't support exclusions; pruned walk is safest |
| 5 | Bash tool exempt from T008 | User request; sandboxing planned separately |
| 6 | T006 uses custom Lipgloss scrollbar | `bubbles/viewport` has no built-in scrollbar |
| 7 | T004/T005 reuse palette overlay pattern | Minimizes new TUI code; consistent UX |
