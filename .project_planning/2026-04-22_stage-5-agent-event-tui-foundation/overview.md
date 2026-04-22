## Request

Plan the implementation for `# Stage 5 - Agent event interface and TUI foundation`, using only that section from `docs/ROADMAP.md` and `docs/INITIAL_IMPLEMENTATION_PLAN.md` as the planning brief.

The work is focused on:

- replacing the current line-oriented interactive REPL with a Bubble Tea TUI
- introducing a renderer-agnostic agent event interface as the primary rendering boundary
- preserving current `--exec` behavior through a plain renderer over the same event stream
- keeping Stage 5 scope constrained to plain-text streaming, a minimal status bar, single-line input, slash commands, resize handling, approval prompts, and scroll wheel support

Relevant code areas identified during bounded repo inspection:

- `internal/output/`
- `internal/agent/`
- `internal/provider/openai_compat.go`
- `internal/tool/executor.go`
- `cmd/steiner/main.go`
- `internal/repl/` for removal after replacement
- `go.mod`

## Overview

Stage 5 should be planned as an architectural refactor first and a UI replacement second.

The repo already has an event sink, a stream-oriented plain terminal renderer, approval events, and an interactive REPL package. That means the main risk is not "adding a TUI"; it is reshaping the existing output and approval plumbing into a renderer-agnostic event boundary without regressing `--exec` or leaking direct terminal writes from agent, provider, and tool code.

The implementation should be staged around four high-level outcomes:

1. Lock the event contract and plain renderer boundary first.
2. Refactor agent, provider, and tool execution paths to emit only structured events and to stop performing direct terminal I/O.
3. Build the Bubble Tea TUI on top of that event stream, with the agent loop running in the background and the TUI owning the main thread.
4. Remove the old REPL only after the TUI and plain renderer cover the required behavior and the no-direct-writes rule is enforced.

The most important architectural choice is to keep the event interface renderer-agnostic. The TUI should not inspect agent state directly, and the plain renderer should not remain a special case hidden inside the old stream code. Both modes should subscribe to the same structured event stream, even if the transport ends up exposing both a channel-oriented and callback-oriented seam internally.

The research result materially supports a message-bridge design for the TUI:

- the agent loop should emit domain events into an internal event stream
- a TUI-side bridge should translate those events into concrete `tea.Msg` values
- Bubble Tea's `Program.Send` should be treated as the final UI injection point, not as the domain event contract itself

This keeps the UI decoupled while still fitting Bubble Tea's documented update model.

The second key design choice is approval flow. Approval interaction should be modeled explicitly as an event request plus a response path back to the blocked execution path. The plan should assume a dedicated approval response mechanism, likely based on a request payload carrying a response channel or equivalent callback handle, rather than reusing ad hoc stdin prompting logic from the current CLI path.

The minimum reliable Stage 5 scope should therefore deliver:

- an expanded typed event model that covers streaming, thinking, tool lifecycle, approval lifecycle, context updates, limits, skills, compaction, completion, and errors
- a plain renderer extracted into a standalone output subscriber and validated as the regression gate for `--exec`
- an agent/provider/tool refactor that removes direct stdout/stderr writes from runtime execution paths
- a new `internal/tui/` package with a minimal Bubble Tea model, viewport-backed content area, single-line input area, bottom status bar, approval mode, resize handling, and scroll wheel support
- deletion of `internal/repl/` only after command behavior and approval handling have been reimplemented in the TUI path

Key constraints and risks that should drive the detailed plan:

- startup and shutdown ordering around Bubble Tea `Program.Send` must avoid deadlock before the program starts
- event transport must not be designed around TUI assumptions only; plain rendering remains a first-class consumer
- approval prompts must not deadlock or interleave confusingly with ongoing streamed output
- high-frequency stream chunks must not force the UI path into unbounded buffering or accidental backpressure on the agent loop
- no runtime code in `internal/agent`, `internal/provider`, or `internal/tool` may keep direct stdout/stderr writes after this stage

Planning assumption:

- the existing `internal/output` event sink and `stream.go` renderer provide the migration starting point rather than being replaced wholesale at once

## Verification Strategy

### Sources
- `AGENTS.md`
- `README.md`
- `Makefile`
- `go.mod`

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### formatting
- preferred_mode: fix
- fix:
  - `gofmt -w ./cmd/steiner ./cmd/steiner-core-tools ./internal/...`
- check:
  - `gofmt -l ./cmd/steiner ./cmd/steiner-core-tools ./internal/...`
- use_check_only_when:
  - when validating without introducing unrelated formatting churn
  - when reviewer verification should confirm cleanliness without mutating files

#### vet
- preferred_mode: check
- fix:
  - none
- check:
  - `go vet ./...`
- use_check_only_when:
  - always; `go vet` has no safe fix mode

#### unit_and_integration_tests
- preferred_mode: check
- fix:
  - none
- check:
  - `go test ./...`
- use_check_only_when:
  - always; tests are validation only

#### build
- preferred_mode: check
- fix:
  - none
- check:
  - `go build ./...`
  - `make build-binaries`
- use_check_only_when:
  - always; build commands are validation only
  - use `make build-binaries` when validating packaged CLI binaries specifically

### Tiers
- cheap:
  - formatting
  - vet
- medium:
  - build
- expensive:
  - unit_and_integration_tests

### Required Boundaries
- step_level_exceptions:
  - preserve a plain-renderer regression gate before starting TUI implementation work
  - validate any approval-contract redesign with focused tests before wiring full TUI interaction
- stage_level_exceptions:
  - do not delete `internal/repl/` until the TUI path covers command handling and approval prompts
  - do not treat Stage 5 as complete until direct stdout/stderr writes are removed from runtime paths outside approved output entry points
- end_of_implementation:
  - formatting
  - vet
  - unit_and_integration_tests
  - build
- reviewer_after_fix:
  - rerun the smallest relevant command first for the touched area
  - rerun the end-of-implementation set before final handoff if runtime-output or UI plumbing changed

### Assumptions
- `gofmt` and `go vet` are repo-mandated before commit based on `AGENTS.md`
- `go test ./...` is the repo-wide test command based on `README.md`
- `go build ./...` is the repo-wide compile check, while `make build-binaries` validates the packaged binaries
- no CI workflow file is present at the repo root, so repo-local docs and task runners are the primary verification evidence

### Uncertainties
- the exact formatter target set may be tightened during execution if a narrower package list is more practical
- the final no-direct-writes enforcement may need a grep-based test helper or a focused unit test strategy rather than relying on manual inspection
- approval-flow verification may require purpose-built integration coverage beyond current REPL-era tests

## Decision Log

- User requested that planning context from the roadmap docs be limited to the `Stage 5 - Agent event interface and TUI foundation` section only.
- Research decision: external research was optional but user explicitly chose to run it.
- Research completed in `.project_planning/2026-04-22_stage-5-agent-event-tui-foundation/research.md`.
- Research confirmed that Bubble Tea's documented message bridge pattern supports keeping the domain event interface separate from the TUI update loop.
- Planning direction is to treat Stage 5 as an event-boundary refactor plus UI replacement, not as an isolated frontend task.
- Plain `--exec` rendering is a regression gate and must be proven before TUI work proceeds deeply.
