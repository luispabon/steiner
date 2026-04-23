# Overview — Stage 7: Theme system and input polish

## Request

Extract all hardcoded TUI colour values into a swappable `Theme` interface backed by Catppuccin Mocha (via `catppuccin/go`). Fix the current Glamour/Lip Gloss colour mismatch where the markdown renderer uses Glamour's built-in `"dark"` style instead of the Catppuccin palette. Upgrade the input area with multi-line editing, command history, readline keybindings, tab completion, and paste handling. Add a help overlay showing keybind reference.

**Additional user requirement (beyond ROADMAP):** the markdown content pane and message area must render with colours that visually match the Catppuccin Mocha chrome — fixing the existing mismatch between Glamour `WithStandardStyle("dark")` and the Lip Gloss palette.

## Overview

### Current state (discovered)

- `render.go`: 13 Catppuccin Mocha hex constants hardcoded, all Lip Gloss styles built inline
- `content.go`: Glamour renderer initialised with `glamour.WithStandardStyle("dark")` — **root cause of colour mismatch**
- `model.go`: uses `bubbles/textinput` (single-line) for input
- `input.go`: pure slash-command parser (54 lines), no widget logic
- No `catppuccin/go` dependency in `go.mod`

### What changes

**Stage 1 — Theme package**

New package `internal/tui/theme/`:

- `theme.go`: `Theme` interface (12–15 semantic colours); `Styles` struct of named Lip Gloss styles for all chrome regions; `LipGlossStyles() Styles` and `GlamourStyleSheet() glamour.TermRendererOption` methods
- `registry.go`: `map[string]Theme` registry; `Register`, `Get`, `Default`
- `catppuccin.go`: Catppuccin Mocha implementation using `github.com/catppuccin/go` (`catppuccingo.Mocha`); palette values accessed as `mocha.Base().Hex` → `lipgloss.Color(hex)`; Glamour style sheet built with `glamour.WithStyles(ansi.StyleConfig{...})` — `*string` hex fields populated directly from palette; `StyleCodeBlock.Theme` set to `"catppuccin-mocha"` if present in Chroma v2.14.0, otherwise fallback to `"dracula"` (risk item)

**Stage 2 — TUI chrome wiring**

Refactor `render.go`, `content.go`, `sidebar.go`, `statusbar.go`, `app.go`, `model.go`:

- Remove all hardcoded hex strings and inline colour calls
- Load theme at startup from `tui.theme` config; fall back to default with warning
- Pass `Styles` and `GlamourStyleSheet()` result to all components
- Glamour renderer rebuilt with theme-derived style sheet → fixes mismatch

**Stage 3 — Input improvements**

Extend `input.go`, `model.go`, `keys.go`:

- Replace `bubbles/textinput` with `bubbles/textarea`; all 5 readline bindings (ctrl+a/e/k/w/u) are native in textarea's `DefaultKeyMap`
- Submit on `Enter` is NOT textarea's default — parent model must intercept `Enter` key and call `textarea.Value()` then reset; `Shift+Enter` or `Alt+Enter` inserts newline
- Command history: `[]string` slice in model; up/down intercepted in parent `Update()` before passing to textarea
- Tab completion: triggered after `/` prefix; completes against known commands + discovered skill names; contextual (no completion without `/`)
- Paste: textarea handles ctrl+v natively; guard against accidental command dispatch on pasted `/`-prefixed lines

**Stage 4 — Help overlay**

New `help.go`; extend `model.go`, `keys.go`:

- `helpVisible bool` flag in model state
- Toggled by `?` key
- Lip Gloss overlay rendered over content pane in `View()` when flag set
- Keybindings grouped: Navigation, Input, Session, Approval
- Dismissed by `?` or `Escape`
- Styles from theme

### Scope constraints (carried from ROADMAP)

- Ship only Catppuccin Mocha — no runtime switching
- Theme config-driven at startup only
- No vim modal editing
- `help.go` overlay only — no separate screen

### Risks

- **Chroma syntax theme**: `catppuccin-mocha` may not exist in Chroma v2.14.0; fallback to `"dracula"` acceptable for Stage 7; proper Chroma theme deferred to Stage 10
- **textarea submit wiring**: Enter must be intercepted at parent model level — easy to get wrong and cause accidental submits or newline suppression
- **help overlay event routing**: `?` key must not fire when input area is focused and user is typing `?` as text; routing needs careful guard

## Verification Strategy

### Sources
- `CLAUDE.md` (project instructions — canonical command list)
- `Makefile` (build-binaries only)
- No CI config found

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: false

### Commands

#### formatter
- preferred_mode: fix
- fix:
  - `gofmt -w <changed files>`
- check:
  - `gofmt -l <changed files>`
- use_check_only_when:
  - repo_wide_formatting_allowed is false and scope is ambiguous

#### build
- preferred_mode: check
- check:
  - `go build ./...`
- use_check_only_when:
  - always (no fix mode for build)

#### vet
- preferred_mode: check
- check:
  - `go vet ./...`
- use_check_only_when:
  - always

#### tests
- preferred_mode: check
- check:
  - `go test ./internal/tui/...`
  - `go test ./...` (end of implementation)
- use_check_only_when:
  - always

#### binaries
- preferred_mode: check
- check:
  - `make build-binaries`
- use_check_only_when:
  - end of implementation only

### Tiers
- cheap:
  - formatter
  - vet
- medium:
  - build
  - tests (package-scoped)
- expensive:
  - tests (full)
  - binaries

### Required Boundaries
- step_level_exceptions:
  - none
- stage_level_exceptions:
  - none
- end_of_implementation:
  - formatter
  - build
  - vet
  - tests (full)
  - binaries
- reviewer_after_fix:
  - rerun `go build ./...` and `go vet ./...` after any fix
  - rerun package tests for any package touched

### Assumptions
- `go test ./...` includes all packages; no separate integration test runner
- `gofmt` covers all formatting requirements (no golangci-lint configured)
- `make build-binaries` validates both cmd binaries link correctly

### Uncertainties
- Whether `catppuccin-mocha` Chroma theme exists in v2.14.0 (research flagged as open question)

## Decision Log

- **Glamour style sheet**: use `glamour.WithStyles(ansi.StyleConfig)` Go struct API, not JSON — direct hex assignment from catppuccin palette, no serialisation round-trip
- **Input widget**: switch from `bubbles/textinput` to `bubbles/textarea`; all readline bindings native; submit and history handled in parent model
- **Chroma fallback**: use `"dracula"` if `catppuccin-mocha"` unavailable in current Chroma version; note as known limitation
- **Help overlay key guard**: `?` fires help only when input area is NOT focused or when input value is empty
- **Stage 7 scope addition**: Glamour/Lip Gloss colour mismatch fix included as explicit deliverable (was implied by ROADMAP but the screenshot confirms it is broken and must be fixed)
