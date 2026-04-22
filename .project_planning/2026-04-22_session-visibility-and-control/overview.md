## Request

Plan the Stage 5 "Session visibility and control" work described in the `## Stage 5 - Session visibility and control` sections of `docs/ROADMAP.md` and `docs/INITIAL_IMPLEMENTATION_PLAN.md`, without expanding scope beyond those sections.

## Overview

Stage 5 is a console usability stage, not a new context-management algorithm stage.

The goal is to make long-running sessions understandable and controllable from the terminal by surfacing existing context diagnostics more clearly, extending session inspection beyond the current minimal `/history`, and improving interruption and cancellation behavior without changing prompt-assembly semantics.

The implementation should stay concentrated in the Stage 5 package boundaries named by the docs:

- `internal/repl/` for new inspection commands, session and turn visibility helpers, and interruption/cancellation UX hooks
- `internal/output/` for concise terminal presentation of context budgets, compaction activity, stop reasons, and recent diagnostics
- `internal/agent/` for only the minimum additional event/state surfaces needed to expose useful diagnostics to users
- `cmd/steiner/` only where the existing CLI-to-REPL wiring needs small changes to pass through or retain the new visibility data

The stage should preserve these design constraints:

- summary-first visibility, not transcript dumps
- no raw prompt internals exposed by default
- deterministic session state when runs are interrupted or cancelled
- new controls fit the existing slash-command REPL model cleanly
- prompt assembly outcomes remain unchanged by the visibility features themselves

Existing repo evidence suggests the core mechanisms already exist and Stage 5 is primarily about exposure and UX:

- conversation compaction already emits diagnostic events
- prompt assembly already emits context budget diagnostics for truncated sources
- the CLI runner already collects context diagnostic events into REPL results
- the current REPL already has a minimal `/history` command and command namespace tests

The most likely implementation shape is:

1. define a clearer terminal-facing summary model for context budgets, compaction, stop reasons, and recent diagnostic events
2. extend the REPL command surface with a small, coherent set of visibility commands rather than ad hoc one-offs
3. add interruption/cancellation hooks that preserve consistent state and leave users with an inspectable reason for what happened
4. add focused tests proving the new visibility features improve inspection and control without altering core agent behavior

## Verification Strategy

### Sources
- `AGENTS.md`
- `README.md`
- `Makefile`
- `internal/repl/commands.go`
- `internal/repl/repl_test.go`

### Defaults
- execution_verification_timing: step_or_stage_exceptions_only
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### formatting
- preferred_mode: fix
- fix:
  - `gofmt -w <changed-go-files>`
- check:
  - `gofmt -d <changed-go-files>`
- use_check_only_when:
  - when verifying formatting drift without modifying files
  - when the step is review-only and should avoid write churn

#### vet
- preferred_mode: check
- fix:
  - none
- check:
  - `go vet ./...`
- use_check_only_when:
  - always, because `go vet` is check-only

#### unit_and_integration_tests
- preferred_mode: check
- fix:
  - none
- check:
  - `go test ./...`
  - `go test ./internal/repl ./internal/output ./internal/agent ./cmd/steiner`
- use_check_only_when:
  - always, because `go test` is check-only
  - prefer targeted package runs during implementation when the change surface is local to Stage 5 packages

#### build
- preferred_mode: check
- fix:
  - none
- check:
  - `go build ./...`
  - `make build-binaries`
- use_check_only_when:
  - always, because build validation is check-only
  - prefer `go build ./...` for faster validation during implementation

### Tiers
- cheap:
  - formatting
- medium:
  - vet
  - unit_and_integration_tests
  - build
- expensive:
  - none

### Required Boundaries
- step_level_exceptions:
  - run targeted `go test` for the package under active change when adding or changing REPL commands, output formatting, or interruption behavior
- stage_level_exceptions:
  - none
- end_of_implementation:
  - formatting
  - vet
  - unit_and_integration_tests
  - build
- reviewer_after_fix:
  - rerun the smallest relevant targeted tests first
  - rerun broader end-of-implementation checks if reviewer fixes touch shared behavior or multiple Stage 5 packages

### Assumptions
- `gofmt` and `go vet` are repo-mandated before commit because `AGENTS.md` states that requirement explicitly
- `go test ./...` is the default broad automated test command because `README.md` documents it directly
- `go build ./...` is the default broad compile check because `README.md` documents it directly
- `make build-binaries` is optional validation for final binary output, not the primary fast-path implementation check
- Stage 5 changes will remain mostly within `internal/repl/`, `internal/output/`, `internal/agent/`, and small CLI wiring in `cmd/steiner/`

### Uncertainties
- no CI configuration is present at the repo root, so there is no stronger repository-wide automation source than the local docs and manifests reviewed here
- interruption/cancellation testing may require slightly broader integration coverage once the exact UX hook points are chosen

## Decision Log

- Research decision: no external research required, because the Stage 5 scope is repo-specific and the core compaction/budget mechanisms already exist locally.
- Scope decision: keep this stage focused on visibility, inspection, and control surfaces; do not redesign compaction or prompt budgeting algorithms unless the implementation reveals a minimal blocking gap.
- UX decision: prefer a small coherent REPL command set and concise summaries over raw internal dumps or highly granular debug commands.
- Verification decision: use targeted package tests during execution, then finish with repo-wide formatting, vet, tests, and build checks.
