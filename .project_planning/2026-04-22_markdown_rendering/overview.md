# Overview

## Request

Implement **Stage 6 - Markdown rendering and status sidebar** from the roadmap: add Glamour markdown rendering to the TUI content pane and implement a status sidebar showing live session metrics.

## Overview

This stage adds two primary features to the existing Bubble Tea TUI:

1. **Markdown rendering** via Glamour with Chroma syntax highlighting for code blocks, supporting full markdown (headers, bold, italic, inline code, code blocks, lists, tables, links). Streaming strategy: completed blocks rendered with full styling, in-progress blocks shown as plain text until block boundary detected.

2. **Status sidebar** as a right-side panel (30-35 chars fixed width) showing: model name, provider endpoint, context tokens used/budget with percentage, current turn/max turns, compaction state, git branch and dirty state, working directory, active skills. Automatic collapse below `tui.sidebar_min_width` threshold, manual toggle via keybind.

Visual hierarchy: assistant prose at full brightness, tool execution and thinking blocks muted/subordinate.

**Dependencies:**
- Add `github.com/charmbracelet/glamour` to go.mod
- Stage 5 TUI already in place (functional Bubble Tea app, event interface, plain renderer)

**Key packages:**
- `internal/tui/content.go` - extend with Glamour integration, block boundary detection
- `internal/tui/sidebar.go` - new, status panel implementation
- `internal/tui/git.go` - new, branch/dirty state detection
- `internal/tui/render.go` - new, hardcoded Catppuccin Mocha styles
- `internal/tui/model.go` - extend for three-region layout

## Verification Strategy

### Sources
- AGENTS.md
- Makefile

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### build
- preferred_mode: fix
- fix:
  - `go build ./...`
  - `make build-binaries`
- check:
  - `go build ./...`

#### test
- preferred_mode: fix
- fix:
  - `go test ./...`
- check:
  - `go test ./...`

#### vet
- preferred_mode: fix
- fix:
  - `go vet ./...`
- check:
  - `go vet ./...`

#### format
- preferred_mode: fix
- fix:
  - `gofmt -w <files>`
- check:
  - `gofmt -l <files>`

### Tiers
- cheap:
  - format
  - vet
- medium:
  - build
- expensive:
  - test

### Required Boundaries
- step_level_exceptions: none
- stage_level_exceptions: none
- end_of_implementation:
  - test
- reviewer_after_fix:
  - Run vet after any fix before committing
  - Run build before test to catch compile errors early

### Assumptions
- Glamour can be integrated without major API changes
- Stage 5 TUI is functional and provides the event interface foundation
- Catppuccin Mocha hex values are available from standard sources

### Uncertainties
- Exact Glamour API for streaming block rendering
- Performance impact of Glamour rendering on each block completion
- Whether existing model.go layout can accommodate three regions without refactor

## Decision Log

| Date | Decision | Rationale |
|------|----------|-----------|
| 2026-04-22 | Skip research | Stage spec provides sufficient detail; dependencies (Glamour, Charm ecosystem) are known and stable; no external APIs |
| 2026-04-22 | Use deferred verification | Standard repo practice per AGENTS.md; stage 6 is feature addition not critical bugfix |