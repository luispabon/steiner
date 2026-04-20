## Request

Plan the implementation work for `docs/ROADMAP.md` and `docs/INITIAL_IMPLEMENTATION_PLAN.md` focused only on `## Stage 1 - Core single-agent loop`.

Constraints:
- Only use the Stage 1 sections of those two docs as the planning basis from that documentation set.
- Respect the repository architecture and package-boundary rules in `AGENTS.md`.
- Treat this as Stage 1 only: no sub-agents, no compaction, no persistence, no advanced `edit` primitive work beyond Stage 1 scope.

## Overview

Stage 1 should deliver the first end-to-end usable `steiner` agent while staying intentionally thin. The implementation should add a single-agent ReAct loop, an OpenAI-compatible provider path, prompt assembly with fixed context ordering, explicit skill discovery/invocation, sequential core tool execution, approval handling, REPL UX, `--exec` mode, and plain terminal/event output.

The work should preserve package boundaries strictly:
- `internal/agent` owns loop orchestration and depends on provider/tool abstractions rather than transport details.
- `internal/prompt` owns preamble, AGENTS loading, bounded project-context loading, skill-context insertion, and final prompt assembly order.
- `internal/provider` owns OpenAI-compatible transport and normalization of streaming and non-streaming responses through one internal response model.
- `internal/tool` owns tool registry/execution policy, approval resolution, and JSON I/O contract enforcement.
- `internal/repl` owns interactive UX only.
- `internal/output` owns terminal/machine-readable output formatting only.

The core delivery is a minimal but complete vertical slice:
- provider call enters through the scheduler
- prompt is assembled in the repository-mandated precedence order
- model responses either terminate with final text or request one tool call
- tool execution remains sequential
- approval rules gate mutating or shell-style tools
- results are surfaced with bounded, plain output and explicit stop reasons

Primary code areas likely in scope:
- `internal/provider/openai_compat.go`
- `internal/agent/loop.go`
- `internal/prompt/system.go`
- `internal/prompt/agents.go`
- `internal/prompt/context.go`
- `internal/prompt/skills.go`
- `internal/prompt/assemble.go`
- `internal/skill/loader.go`
- `internal/tool/executor.go`
- `internal/tool/approval.go`
- `internal/repl/repl.go`
- `internal/repl/commands.go`
- `internal/repl/completer.go`
- `internal/output/stream.go`
- `cmd/steiner-core-tools/main.go`
- `cmd/steiner-core-tools/read.go`
- `cmd/steiner-core-tools/write.go`
- `cmd/steiner-core-tools/glob.go`
- `cmd/steiner-core-tools/search.go`
- `cmd/steiner-core-tools/bash.go`

Key implementation constraints and risks:
- Do not let prompt assembly or tool result handling grow unbounded; even before Stage 3, Stage 1 must enforce hard context limits for project-context loading.
- Do not let skills gain system-level authority; they remain auxiliary context below AGENTS.md and above conversation history.
- Do not blur package boundaries while wiring the loop together.
- Do not overbuild terminal UX; REPL should stay minimal and functional.
- Do not make `write` the only plausible future mutation path; Stage 1 can use it, but the design should not block Stage 2 `edit`.
- Keep behavior predictable with `provider.parallelism: 1`, especially around scheduling and sequential tool execution.

Acceptance focus for execution planning:
- small toy-repo bugfix flow works end to end
- one-file mutation path works through current Stage 1 tooling
- targeted command/test execution works under approvals
- stop conditions are explicit and deterministic
- prompt assembly and skill injection rules are test-covered

## Verification Strategy

### Sources
- `AGENTS.md`
- `go.mod`
- existing tests under `cmd/steiner` and `internal/`

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
  - the user requests a diff-only verification pass
  - a step is explicitly analysis-only and should not mutate files

#### vet
- preferred_mode: check
- fix:
  - none
- check:
  - `go vet ./...`
- use_check_only_when:
  - always; `go vet` has no safe repository fix mode

#### unit-and-integration-tests
- preferred_mode: check
- fix:
  - none
- check:
  - `go test ./...`
  - `go test ./internal/... ./cmd/...`
- use_check_only_when:
  - always; tests are validation, not a safe fix tool
  - a narrower package-targeted run gives sufficient signal during intermediate steps

#### build
- preferred_mode: check
- fix:
  - none
- check:
  - `go build ./...`
- use_check_only_when:
  - always; build validation is check-only

### Tiers
- cheap:
  - formatting
- medium:
  - unit-and-integration-tests
  - build
- expensive:
  - vet

### Required Boundaries
- step_level_exceptions:
  - run focused package tests when a step changes loop, provider, prompt, or tool behavior materially
  - run `gofmt -w` on touched Go files before concluding any implementation step that edits Go code
- stage_level_exceptions:
  - none
- end_of_implementation:
  - formatting
  - build
  - unit-and-integration-tests
  - vet
- reviewer_after_fix:
  - rerun the minimal relevant package tests first
  - rerun end-of-implementation checks if fixes affect shared behavior or verification infrastructure

### Assumptions
- `AGENTS.md` is the repository source of truth for required verification expectations.
- `gofmt` and `go vet` are required before commit for all Go changes.
- `go test ./...` is the default broad validation command because there is no higher-level task runner or CI config in the repository root.
- current repository scope is small enough that `go build ./...` and `go test ./...` are still reasonable end-of-implementation checks.

### Uncertainties
- there is no root `Makefile`, CI workflow, or lint configuration checked in yet, so no stronger repo-wide lint command was discoverable
- integration and golden test coverage described in planning docs may not all exist yet and may need to be created during Stage 1 execution
- command cost tiers are inferred from the current repository shape and may need refinement if additional packages or heavy fixtures appear during implementation

## Decision Log

- Loaded only the `## Stage 1 - Core single-agent loop` section from `docs/ROADMAP.md`.
- Loaded only the `## Stage 1` section from `docs/INITIAL_IMPLEMENTATION_PLAN.md`.
- Skipped external research because the Stage 1 scope, constraints, deliverables, and tests are already explicit in repo-local planning docs.
- Derived verification strategy from `AGENTS.md`, `go.mod`, and the currently present test files because no root task runner or CI workflow is present.
- Chose a single Stage 1 planning track focused on the first usable vertical slice rather than splitting planning across package-by-package milestones.
