## Request

Plan implementation work for backlog tickets `T025` through `T028` from `.project_planning/BACKLOG.md`:

- `T025` - ESC does not stop streaming or cancel conversation
- `T026` - Tool approvals broken during streaming
- `T027` - Ctrl+C does not work during active conversation
- `T028` - Shift+Enter does not work

Constraints and expectations:

- Keep package boundaries intact, especially between `cmd/steiner`, `internal/tui`, and `internal/agent`.
- Treat this as interactive-mode behavior only unless code inspection proves otherwise.
- Keep changes minimal and backed by nearby unit tests.
- Run `gofmt -w` after Go edits and prefer targeted tests before broader checks.

## Overview

The interactive TUI has a narrow cluster of input-routing bugs centered on active streaming and prompt editing.

Observed code-level shape:

- [`internal/tui/model_update.go`](../../../internal/tui/model_update.go) blocks all non-`Esc` key input while `streamingPhase != ""`.
- The current `Esc` handler only mutates local TUI state by appending an interrupted marker and resetting status text; it does not propagate cancellation to the active run context.
- Because the streaming gate runs before the normal `Ctrl+C` handling and before approval submission via `Enter`, active conversations can trap the UI in a state where cancellation and approval interaction do not reach runtime logic.
- [`cmd/steiner/interactive.go`](../../../cmd/steiner/interactive.go) already owns the long-lived interactive loop and run context, so the missing behavior is likely explicit TUI-to-runtime interrupt plumbing rather than provider-layer cancellation support.
- `T028` is adjacent in the same input stack but likely distinct: the textarea keymap claims to bind `shift+enter`, so the failure is probably in key routing, textarea configuration, or Bubble Tea event handling rather than the streaming cancellation path.

Planning direction:

- Keep `T025`, `T026`, and `T027` in one implementation slice focused on active-run interruption and allowing prompt/approval interaction while streaming.
- Keep `T028` in a separate follow-on slice unless implementation proves it is the same defect. This reduces merge risk and keeps acceptance criteria testable.
- Favor tests at the TUI model layer first, then add a thin interactive/runtime test only where cancellation wiring crosses package boundaries.

Primary risks:

- Fixing streaming input inhibition too broadly could allow ordinary prompt submission during active runs when only cancellation and approval interaction should be possible.
- Cancellation must stop the actual agent run, not just clear local streaming indicators, or `T025` and `T027` will regress silently.
- `Shift+Enter` may depend on terminal/event semantics that require careful test coverage to avoid false confidence from unit-only validation.

## Verification Strategy

### Sources
- `AGENTS.md`
- `Makefile`
- `go.mod`

### Defaults
- execution_verification_timing: step_or_stage_exceptions_only
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### gofmt
- preferred_mode: fix
- fix:
  - `gofmt -w <files>`
- check:
  - `gofmt -d $(git ls-files '*.go')`
- use_check_only_when:
  - validating formatting without mutating files
  - reviewing broad repository state where formatting churn would be out of scope

#### targeted_go_tests
- preferred_mode: check
- fix:
  - none
- check:
  - `go test ./internal/tui -run TestName`
  - `go test ./cmd/steiner -run TestName`
- use_check_only_when:
  - always; `go test` has no safe fix mode

#### repo_wide_go_tests
- preferred_mode: check
- fix:
  - none
- check:
  - `go test ./...`
- use_check_only_when:
  - always; use after targeted checks or at end of implementation

#### go_vet
- preferred_mode: check
- fix:
  - none
- check:
  - `go vet ./...`
- use_check_only_when:
  - always; no safe fix mode is provided

#### build_binaries
- preferred_mode: check
- fix:
  - none
- check:
  - `go build ./...`
  - `make build-binaries`
- use_check_only_when:
  - always; use the narrower `go build ./...` first unless binary packaging validation is specifically needed

### Tiers
- cheap:
  - gofmt
  - targeted_go_tests
- medium:
  - go_vet
  - build_binaries
- expensive:
  - repo_wide_go_tests

### Required Boundaries
- step_level_exceptions:
  - run targeted tests for `internal/tui` and any touched interactive-runtime tests when changing key handling or cancellation wiring
- stage_level_exceptions:
  - apply `gofmt -w` to touched Go files at the end of each implementation step
- end_of_implementation:
  - repo_wide_go_tests
  - go_vet
  - build_binaries
- reviewer_after_fix:
  - rerun the most specific failing or coverage-relevant test first
  - rerun end-of-implementation checks only after code changes stabilize

### Assumptions
- `go test ./internal/tui` and `go test ./cmd/steiner` are sufficient targeted entry points for this bug batch.
- `go build ./...` is a useful cheaper compile validation before `make build-binaries`.
- No CI-only or hidden lint step exists beyond repo instructions and `Makefile`.

### Uncertainties
- The exact minimal test target names will depend on whether new coverage lands in existing test files or new ones.
- `T028` may require a different validation surface if the bug depends on terminal key event translation rather than pure model logic.

## Decision Log

- Research skipped: the task is repo-local, current code inspection exposed the relevant fault boundaries, and external information is unlikely to change the plan materially.
- Scope includes `T025` through `T028` as requested because they share the interactive prompt/input surface and are contiguous in the backlog section.
- Expected execution split is two-stage: first active-run interruption and approval behavior (`T025`-`T027`), then multiline input behavior (`T028`), unless implementation reveals a single shared fix.
