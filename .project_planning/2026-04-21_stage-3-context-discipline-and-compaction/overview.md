## Request

Plan the implementation of Stage 3, `Context discipline and compaction`, using only the Stage 3 sections of `docs/ROADMAP.md` and `docs/INITIAL_IMPLEMENTATION_PLAN.md` as the roadmap source for scope. The focus is to make long sessions viable by introducing explicit context-source budgeting, rolling conversation compaction, structured tool-result summaries, preservation of active constraints and unresolved work, and user-visible diagnostics for compaction/debugging.

## Overview

Stage 3 should be treated as the point where `steiner` stops treating prompt assembly as append-only state and starts treating it as a bounded policy-driven system. The current implementation already respects the required high-level context ordering in `internal/prompt/assemble.go`, but it still appends `Conversation` and `ToolResults` naively and only applies a simple byte budget to auto-discovered project files. `internal/agent/state.go` currently stores the full conversation without any compacted representation, and the REPL/output surfaces do not yet expose context-diagnostics or history inspection for compaction-aware debugging.

The implementation should therefore center on three coordinated changes. First, `internal/prompt` needs a real Stage 3 policy layer: explicit source categories and budgets, retention rules for recent turns, compaction logic for older turns, and structured summary blocks for both compacted conversation segments and oversized tool outputs. Second, `internal/agent` needs a context-state model that preserves durable user constraints, active task/file focus, unresolved tasks, and retained summaries without leaking prompt-assembly policy into unrelated packages. Third, user-visible and log-visible diagnostics should be added in the appropriate surfaces: prompt/context compaction events in `internal/output`, and a minimal REPL history/debug entry point that exposes when compaction happened and what was retained versus truncated.

The plan should preserve the architecture invariants from `AGENTS.md`. In particular:

- context source precedence must remain fixed
- skills must stay auxiliary, not elevated to system authority
- `internal/prompt` and `internal/agent` must remain separate packages with hard boundaries
- tool summaries must never become instruction-bearing authority
- Stage 3 must be complete enough that Stage 4/5 delegation can rely on bounded parent context rather than raw transcript growth

The main technical risk is not implementing compaction itself, but implementing it in a way that drops actionable state. The design should bias toward preserving durable intent in typed internal state and rendering that state into bounded prompt blocks, instead of repeatedly summarizing raw transcript text. Another risk is over-coupling debug/history features to internal prompt data structures; the REPL surface should stay thin and consume exported diagnostics/state rather than reconstructing prompt logic itself.

At this checkpoint, the likely implementation shape is:

- extend `internal/prompt` with Stage 3-specific budgeting, retention, summary, and compaction types/files
- add an agent-owned context state model in `internal/agent/context_state.go`
- thread compacted state and tool-summary envelopes through the loop without breaking existing provider-facing message semantics
- add output events or payload extensions for compaction diagnostics
- add focused unit, integration, and golden coverage around bounded growth and preservation of active constraints

Open planning questions remain at implementation detail level, not scope level. The later detailed plan should decide exact type boundaries and whether REPL history lands as `/history` immediately or as the minimum built-in command shape needed to satisfy the Stage 3 debug requirement without preemptively designing a broader prompt inspection UX.

## Verification Strategy

### Sources
- `AGENTS.md`
- `Makefile`
- `go.mod`
- `internal/prompt/assemble_test.go`

### Defaults
- execution_verification_timing: step_or_stage_exceptions_only
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### formatting
- preferred_mode: fix
- fix:
  - `gofmt -w <touched-go-files>`
- check:
  - `gofmt -d <touched-go-files>`
- use_check_only_when:
  - a diff-only review signal is needed without mutating files
  - formatting validation is being discussed before implementation begins

#### vet
- preferred_mode: check
- fix:
  - none
- check:
  - `go vet ./...`
- use_check_only_when:
  - always, because `go vet` has no repo-specific safe fix mode

#### unit_and_integration_tests
- preferred_mode: check
- fix:
  - none
- check:
  - `go test ./...`
- use_check_only_when:
  - always, because test runs validate behavior but do not provide a safe automatic fix mode

#### build
- preferred_mode: check
- fix:
  - none
- check:
  - `make build-binaries`
  - `go build ./cmd/steiner ./cmd/steiner-core-tools`
- use_check_only_when:
  - always, because compile/build validation is check-only

### Tiers
- cheap:
  - formatting
- medium:
  - vet
  - unit_and_integration_tests
  - build
- expensive:
  - none identified

### Required Boundaries
- step_level_exceptions:
  - run focused `go test` targets for prompt/agent/output/repl packages when a step changes behavior in those areas enough to need immediate validation before proceeding
- stage_level_exceptions:
  - none
- end_of_implementation:
  - formatting
  - vet
  - unit_and_integration_tests
  - build
- reviewer_after_fix:
  - rerun the narrowest failing or impacted `go test` scope first
  - rerun broader end-of-implementation checks only after targeted fixes are stable

### Assumptions
- `go test ./...` is the primary automated validation command because no narrower repo-wide test runner is defined in the current shallow discovery surface
- `make build-binaries` is the preferred build validation entry point because it reflects the repo’s declared binary outputs
- there is no CI-specific verification policy file in the repository root at present

### Uncertainties
- Stage 3 may justify adding new golden snapshot workflows that could create narrower preferred test commands during execution
- a future REPL command for history/prompt diagnostics may merit its own targeted tests beyond the currently visible package tests

## Decision Log

- Research was skipped because the planning problem is fully grounded in local repository docs and current code structure; external research was unlikely to change scope or architecture materially.
- Scope is intentionally limited to the Stage 3 sections of the two roadmap documents and the minimum repository/code inspection needed to produce a reliable overview and verification strategy.
- The overview treats `internal/prompt` as the primary owner of assembly, budgeting, and compaction policy, while `internal/agent` owns durable execution state that must survive compaction.
- Verification defaults favor deferred broad checks, with focused package tests used as step-level exceptions when a change needs earlier confirmation.
