## Request

Address two prompt overlay annoyances in the TUI:

- When typing inside the slash-command picker or `@` file picker, focus effectively shifts to the overlay and typing no longer behaves like continuing in the prompt box.
- Picker search should support fuzzy matching instead of the current literal substring-style filtering.

## Overview

This change is scoped to the interactive TUI overlay flow under `internal/tui`. The current event routing sends key input directly to open overlays in `handleOverlayKeyMsg`, and both `slashOverlay` and `filePickerOverlay` maintain their own query state and candidate filtering. That produces the current behavior where typing is handled as overlay-local state instead of as an extension of the prompt composer, even though the user expectation is that they are still typing into the prompt while using the picker.

The likely implementation direction is:

- keep the composer input as the source of truth for typed text while a picker is open
- derive picker query text from the relevant prompt token instead of storing an independent overlay-only query
- preserve overlay navigation and selection behavior for arrow keys, enter, and escape
- replace current contains-based filtering with ranked fuzzy matching for both slash items and file entries
- plan around a library-backed matcher rather than a custom scorer, with `github.com/sahilm/fuzzy` as the pragmatic default and `github.com/junegunn/fzf/src/algo` as the higher-fidelity fallback if implementation evidence justifies it
- keep package boundaries intact by containing the behavior inside `internal/tui`

Expected user-visible outcomes:

- typing after `/` keeps building the command text in the composer while the slash overlay updates in place
- typing after `@` keeps building the prompt text in the composer while the file picker updates in place
- fuzzy search matches non-prefix inputs such as abbreviated command names and partial path segments
- the chosen matcher improves ranking quality without forcing a rewrite of the existing Bubble Tea overlay UI
- selection and insertion behavior remain predictable and test-covered

Primary risks:

- breaking existing command parsing or enter-to-submit behavior
- introducing inconsistencies between overlay query display and actual input contents
- changing result ordering in ways that make overlays feel less stable or harder to scan
- selecting a matcher that is easy to wire in but does not feel close enough to the intended `fzf`-style interaction

## Verification Strategy

Repository-level evidence from `Makefile` and repo instructions indicates these verification commands:

- `gofmt -w <files>`: cheap, required after Go edits
- `goimports -w <files>`: cheap, required after Go edits when available
- `go test ./internal/tui -run <targeted tests>`: cheap, first pass for picker and composer regressions
- `go test ./...`: medium, broader regression pass once targeted tests are green
- `make check`: expensive, repo-mandated final verification before closeout; includes tidy, format/import checks, build, tests, race tests, vet, lint, and vuln checks

Safe execution order for implementation and review:

1. format changed Go files with `gofmt -w` and `goimports -w`
2. run targeted `internal/tui` tests covering overlay input routing, filtering, and selection behavior
3. run `go test ./...` if the targeted scope passes
4. run `make check` before finalizing Go changes

## Decision Log

- Research decision: completed after clarification
- Reason: third-party fuzzy-matching library selection affects plan shape and dependency choice
- Research artifact: `.project_planning/2026-05-21_prompt_overlay_picker_improvements/research.md`
- Research recommendation: prefer `github.com/sahilm/fuzzy` for the initial implementation plan; keep `github.com/junegunn/fzf/src/algo` as the fallback if closer `fzf` behavior becomes more important than integration cost
- Planned branch: `cl/2026-05-21_prompt_overlay_picker_improvements`
- Planning folder: `.project_planning/2026-05-21_prompt_overlay_picker_improvements`
