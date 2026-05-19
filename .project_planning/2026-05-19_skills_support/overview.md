## Request

Add multi-root skills support to steiner with explicit-only invocation (no context pollution), slash-command triggering, autocomplete overlay, and session-sticky persistence.

Requirements:
- Skill storage in `~/.agents/skills`, `~/.config/steiner/skills`, and `<project>/.steiner/skills`
- Skills must NOT be added to the context index — no skill catalog in the system prompt
- Skills invoked explicitly via `/skillname` slash commands only
- Autocomplete overlay appears when user types `/` prefix, showing matching skills + built-in commands
- Invoked skills persist in context (session-sticky) until explicitly disabled or `/clear`
- Precedence on name conflict: project > user-config > global (`~/.agents/skills`)

## Overview

### Current State

- `internal/skill/Loader` discovers skills from a single `RootDir` by scanning for `<name>/SKILL.md` directories
- `internal/prompt/skills.go` hardcodes `~/.config/steiner/skills` as `DefaultSkillsRoot`
- Skills are loaded into prompt assembly as `user`-role messages tagged `ContextSourceSkill` when their names appear in `AssemblyOptions.SkillNames`
- TUI has `/skill +name` / `/skill -name` toggle, tab completion, and a `SetSkillEnabled` interactive action
- No overlay for skill discovery — only tab cycling through candidates

### Design

**Multi-root skill discovery**

Extend `internal/skill/Loader` to support multiple root directories. Discovery scans all roots, merges results with precedence (project > user > global), and deduplicates by name (first-found in precedence order wins).

Roots resolved at startup:
1. `<project-root>/.steiner/skills` (project-local)
2. `~/.config/steiner/skills` (user, existing default)
3. `~/.agents/skills` (global cross-agent)

Each root scanned same way as today: directories containing `SKILL.md`.

**No context-index injection**

Skill names/descriptions are never injected into the system prompt. The model has no awareness of available skills. Discovery is purely through the TUI overlay.

**Slash-command invocation**

When user types `/skillname [args]`, the input parser:
1. Checks if the text after `/` matches a known skill name
2. If matched, loads the skill content and enables it (session-sticky)
3. If args are present after the skill name, those become the user message for the current turn
4. If no args, the skill is activated silently (enabled for subsequent turns)

This replaces the current `/skill +name` pattern for activation. `/skill -name` remains for deactivation.

**Autocomplete overlay**

When user types `/`, an overlay appears showing filtered matches across:
- Built-in commands (`/clear`, `/compact`, `/context`, etc.)
- Available skills (from all roots, with source indicator)

The overlay uses the existing `OverlayShell` infrastructure (bottom-anchored, same pattern as file picker). Typing narrows the filter. Arrow keys navigate. Enter selects. Esc dismisses.

This replaces the current tab-cycling completion for `/` commands.

**Session-sticky persistence**

Once a skill is invoked, its `SKILL.md` content is added as a `ContextSourceSkill` user-role message in prompt assembly. It stays in context until:
- User runs `/skill -name`
- User runs `/clear`
- Session ends

This matches the existing persistence model and aligns with Goose's recipe-instructions pattern (research finding).

**Skill file format**

No change — skills remain `<name>/SKILL.md` directories. Markdown content, no required frontmatter. This matches the ecosystem consensus (OpenCode, Cline) and keeps things simple for v1.

### Packages Affected

- `internal/skill/` — multi-root Loader, merged discovery
- `internal/prompt/` — remove `DefaultSkillsRoot` single-path helper, accept multiple roots
- `internal/tui/` — new slash-command overlay, replace tab completion for `/` prefix, direct skill invocation via `/skillname`
- `internal/tui/input.go` — parser changes for `/skillname` recognition
- `cmd/steiner/` — wire multi-root skill paths into Loader and TUI config

### Out of Scope

- Skill parameters / typed arguments (Goose recipe-style)
- Remote/URL skill distribution (OpenCode `.well-known/skills/`)
- Always-on instructions layer (separate feature from skills)
- Skill frontmatter parsing (model overrides, permissions)
- Auto-routing / intent detection

## Verification Strategy

### Sources
- CLAUDE.md (project instructions)
- Makefile
- `.github/workflows/checks.yml`
- `.golangci.yml`

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### formatting
- preferred_mode: fix
- fix:
  - `gofmt -w <changed-files>`
  - `goimports -w <changed-files>`
- check:
  - `make fmt-check`
  - `make imports-check`
- use_check_only_when:
  - CI validation or reviewer pass

#### lint
- preferred_mode: check
- fix:
  - n/a (golangci-lint has no safe auto-fix for all linters)
- check:
  - `golangci-lint run ./...`
- use_check_only_when:
  - always check-only

#### unit-tests
- preferred_mode: check
- fix:
  - n/a
- check:
  - `go test ./internal/skill/...`
  - `go test ./internal/prompt/...`
  - `go test ./internal/tui/...`
  - `go test ./...`
- use_check_only_when:
  - always check-only

#### race-tests
- preferred_mode: check
- fix:
  - n/a
- check:
  - `go test -race ./...`
- use_check_only_when:
  - always check-only

#### vet
- preferred_mode: check
- fix:
  - n/a
- check:
  - `go vet ./...`
- use_check_only_when:
  - always check-only

#### build
- preferred_mode: check
- fix:
  - n/a
- check:
  - `make build-binaries`
- use_check_only_when:
  - always check-only

#### tidy
- preferred_mode: fix
- fix:
  - `go mod tidy`
- check:
  - `make tidy-check`
- use_check_only_when:
  - CI validation

#### vuln
- preferred_mode: check
- fix:
  - n/a
- check:
  - `govulncheck ./...`
- use_check_only_when:
  - always check-only

#### full-check
- preferred_mode: check
- fix:
  - n/a
- check:
  - `make check`
- use_check_only_when:
  - end of implementation only

### Tiers
- cheap:
  - formatting
  - vet
  - build
- medium:
  - lint
  - unit-tests
  - tidy
- expensive:
  - race-tests
  - vuln
  - full-check

### Required Boundaries
- step_level_exceptions:
  - none
- stage_level_exceptions:
  - none
- end_of_implementation:
  - full-check
- reviewer_after_fix:
  - run targeted unit tests for changed packages
  - run lint on changed packages

### Assumptions
- Go 1.25 toolchain available
- golangci-lint, goimports, govulncheck installed (via `make install-check-tools`)
- CLAUDE.md mandates `make check` before finalizing

### Uncertainties
- None identified

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | No skill catalog in system prompt | User requirement. No peer agent does this either (research confirmed). Explicit invocation only. |
| 2 | Session-sticky persistence | Prompt cache stability + multi-turn skill workflows (e.g. coding-loop-planner). Aligns with Goose recipe model. |
| 3 | Skill content as user-role messages | Multiple system messages break some provider APIs. User-role messages are already the steiner pattern. |
| 4 | Precedence: project > user > global | Closest scope wins. Standard pattern across all peers (OpenCode, Cline, Continue). |
| 5 | Three roots: .steiner/skills, ~/.config/steiner/skills, ~/.agents/skills | User requirement. ~/.agents/skills enables cross-agent skill sharing. |
| 6 | Overlay replaces tab-cycling for / commands | Better discoverability. Matches Continue and OpenCode UX patterns. |
| 7 | /skillname as direct invocation | Simpler than /skill +name. Aligns with Goose and Continue slash-command patterns. |
| 8 | No frontmatter parsing for v1 | Keep it simple. Skill parameters, model overrides are out of scope. |
