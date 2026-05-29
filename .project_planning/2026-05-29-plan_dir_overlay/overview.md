## Request

Move the planning docs directory from `.project_planning/` to `.steiner/plans/`, update all skill references accordingly, add `.steiner/plans/` to `.gitignore`, and provide a TUI overlay picker that lists available plan directories when the user types `/implement` or `/review` (triggered after a space or tab, similar to the model picker).

## Overview

The change touches three areas:

1. **Skill markdown and repo docs**: Replace every reference to `.project_planning/` with `.steiner/plans/` in `skills/plan/SKILL.md`, `skills/implement/SKILL.md`, `skills/review/SKILL.md`, and `AGENTS.md`. Update `.gitignore` so the new directory is ignored by default.

2. **TUI overlay for plan listing**: Add a new bottom-anchored overlay (reusing the existing `file_picker` or a new `plan_picker`) that scans `.steiner/plans/` for subdirectories and lets the user select one. The overlay should open after the user types `/implement ` or `/review `, analogous to how `/model ` opens the model picker.

3. **Tab completion integration**: Ensure that when `/implement` or `/review` is Tab-completed and followed by a space, the plan picker opens automatically.

4. **Fix `/model` picker bug**: The model picker fails to open when `/model` is selected from the slash overlay via Tab or Enter. The same root cause would affect `/implement` and `/review`, so we fix it for all three commands.

The plan directory path is hardcoded to `.steiner/plans/`.

## Verification Strategy

| Command | Purpose | Cost | Notes |
|---------|---------|------|-------|
| `gofmt -w <files>` | Format new/modified Go | Cheap | Run after any Go edit |
| `goimports -w <files>` | Fix imports | Cheap | Run after any Go edit |
| `go build ./cmd/steiner` | Compile binary | Cheap | Must pass |
| `go test ./internal/tui/...` | TUI unit tests | Medium | Must pass |
| `go test ./...` | Full test suite | Medium | Should pass |
| `make check` | Full validation | Expensive | Final gate |

## Decision Log

- **Path**: Hardcoded to `.steiner/plans/` (user choice).
- **Overlay trigger**: After space/tab following `/implement` or `/review`, not on slash overlay selection (user choice).
- **Picker trigger**: Open after space (or Tab/Enter selection from slash overlay) on `/implement`, `/review`, and `/model`.
- **Picker UX**: Plan picker header shows the triggering command (`/implement` or `/review`).
- **`/model` bug**: Slash overlay Enter/Tab handler must also trigger pickers instead of only the `default` rune handler.
- **Picker type**: New `plan_picker` overlay following the `model_picker` pattern, because plan selection is simpler than file selection (just directory names) and should match the model picker UX.
