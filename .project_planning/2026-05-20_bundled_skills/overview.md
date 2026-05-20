## Request

Embed the three bundled skills (`plan`, `implement`, `review`) into the steiner binary so they are always available as highest-priority, non-overridable skills. Parse YAML frontmatter (`name`, `description`) for all skills and use `description` as the skill summary.

## Overview

### Embed bundled skills into the binary

Create a `skills/` Go package (`skills/embed.go`) with a `//go:embed */SKILL.md` directive exporting an `embed.FS`. This is the only way to embed files at repo root — Go embed paths cannot use `..`.

### Extend the Loader to support bundled skills

Add a `BundledFS fs.FS` field to `skill.Loader`. When set, bundled skills are discovered and loaded first with source `"bundled"`. Their names are added to the `seen` map before any filesystem root is scanned, making them non-overridable.

On collision (filesystem skill has same name as a bundled skill), log a `slog.Warn` to the log file. No TUI-visible warning.

### Parse YAML frontmatter for all skills

Replace the `discoverSummary` heuristic with proper frontmatter parsing. Extract `name` and `description` from YAML frontmatter. Use `description` as `Skill.Summary` when present. Fall back to the existing first-prose-line heuristic when no frontmatter or no `description` field.

Uses `gopkg.in/yaml.v3` (already a dependency).

### Wire bundled FS in the composition root

`cmd/steiner/runtime_build.go` passes `skills.FS` to the Loader when constructing it.

### Key invariants

- Bundled skills always win. They are reserved names — filesystem skills with the same name are silently shadowed (with a log warning).
- Source label for bundled skills: `"bundled"`.
- Bundled skills have `Path` set to their embedded path (e.g. `plan/SKILL.md`), not a filesystem path.
- Frontmatter parsing applies to all skills (bundled and filesystem), not just bundled.
- No new external dependencies.

### Packages touched

| Package | Change |
|---|---|
| `skills/` (new) | Thin embed-only package exporting `FS` |
| `internal/skill/` | `BundledFS` on Loader, frontmatter parsing, bundled discovery/load, collision warning |
| `cmd/steiner/` | Wire `skills.FS` into Loader |

### Out of scope

- New CLI subcommands or flags.
- Changes to prompt assembly, budget, or context rendering.
- Changes to delegation or sub-agent skill handling.
- Skill content changes (the SKILL.md files themselves are taken as-is).

## Verification Strategy

| Command | Cost | Mode |
|---|---|---|
| `gofmt -w` / `goimports -w` | cheap | fix |
| `go build ./...` | cheap | check |
| `go vet ./...` | cheap | check |
| `go test ./internal/skill/...` | cheap | check |
| `go test ./...` | medium | check |
| `go test -race ./...` | medium | check |
| `golangci-lint run ./...` | medium | check |
| `govulncheck ./...` | expensive | check |
| `make check` | all of the above | combined |

Prefer `go test ./internal/skill/...` during development. Run `make check` before handoff.

## Decision Log

| # | Decision | Reason |
|---|---|---|
| D1 | Embed via `skills/embed.go` package at repo root | Go embed paths are relative to source file; `internal/skill/` cannot reach `../../skills/` |
| D2 | Bundled skills are highest priority, non-overridable | User requirement — bundled skill names are reserved words |
| D3 | Collision logs `slog.Warn`, no TUI warning | User requirement — log-file-only visibility |
| D4 | Parse YAML frontmatter for `description` in all skills | Existing `discoverSummary` ignores the standard `description` frontmatter field |
| D5 | Source label `"bundled"` for embedded skills | Distinguishes from positional filesystem labels (project/user/global) |
| D6 | `BundledFS fs.FS` field on Loader (injected, not global) | Keeps Loader testable; tests can supply a test FS or nil |
| D7 | Fall back to first-prose-line when no frontmatter description | Backward compatibility with skills that lack frontmatter |
