## Request

Turn `.project_planning/CONTEXT_MANAGEMENT_IMPLEMENTATION_PLAN.md` into an execution-ready planning bundle for steiner's context-management work without implementing the feature yet.

The requested scope is the three-part change set described in that source plan:

- stage 1: add a per-path write-generation counter to file annotation tracking so annotation reuse is invalidated by steiner-originated writes even when `mtime` does not move
- stage 2: replace rolling masking with epoch-based masking so the masked prefix stays byte-stable between epoch advances and resets cleanly after compaction
- stage 3: add tiered scratchpad modes, making `scaffold_only` the default and retaining a reduced `hybrid` mode for model-written scratchpad updates

Constraints and repo expectations:

- planning artifacts must live only under `.project_planning/2026-05-02_context-management-implementation/`
- no implementation files are modified during planning
- package boundaries from `AGENTS.md` stay intact, especially between `internal/agent`, `internal/prompt`, and `internal/config`
- any new functionality should come with nearby unit and integration coverage where the repo already has context-management tests

## Overview

This work should stay as a three-stage execution plan because the dependency boundaries are real and match the current code layout:

- stage 1 is the correctness fix and config-independent foundation for file observation state
- stage 2 changes masking semantics in the smart context manager and prompt assembly path
- stage 3 changes scratchpad/config plumbing and post-turn inference behavior without depending on stage 1's file-generation mechanics

Expected code areas:

- `internal/agent/file_tracker.go` and `internal/agent/file_tracker_test.go` for read tracking and annotation invalidation
- `internal/agent/context_manager.go`, `internal/agent/context_manager_test.go`, and `internal/agent/context_management_integration_test.go` for smart-manager state, ingestion, pre-assembly, and diagnostics
- `internal/agent/scratchpad.go`, `internal/agent/scratchpad_test.go`, and related tool-result handling for scratchpad-mode restructuring
- `internal/prompt/masking.go` and `internal/prompt/masking_test.go` for masking behavior
- `internal/config/config.go`, `internal/config/defaults.go`, `internal/config/validate.go`, and config tests for the new `scratchpad_mode` and any context-management validation changes
- `internal/output` event constructors or adjacent event definitions if new epoch or richer file-annotation diagnostics are introduced

Execution shape:

- implement stage 1 first and verify it independently because it closes a concrete correctness gap and introduces state that later stages can reference
- stage 1 is a merge-order preference and safety-first starting point, not a hard code dependency for stage 3
- after stage 1 merges, stage 2 and stage 3 should be treated as parallel-eligible execution packets
- keep event and diagnostics work inside the stage that introduces the behavior, rather than as a later cleanup pass, so observability stays aligned with behavior changes

Main planning decisions:

- preserve the requested three-stage structure instead of collapsing work into a single context-manager refactor
- keep config and validation changes with the stage that consumes them, except for shared smart-context scaffolding that is clearly prerequisite work
- treat stage 2 context-pressure triggering as conditional: if the current token-estimation hook is not available at the right point in `PreAssembly`, plan the turn-count trigger as the required baseline and the pressure trigger as part of the same step only if it fits the existing architecture cleanly
- stage 2 should extend the existing `context_diagnostics` masking payload rather than introduce a separate epoch event type
- stage 3 pivot inference must bypass the normal scratchpad-injection assembly path because `internal/agent/message_convert.go` currently appends the synthetic scratchpad user message for normal requests
- stage 3 shared decision extraction should be implemented as mode-agnostic infrastructure first, with only the merge of model-written decisions remaining hybrid-specific
- stage 3 hybrid-mode schema reduction is a compatibility-sensitive change: parsing must tolerate unknown legacy fields, ignore them, and emit a warning instead of failing silently or rejecting the tool result

Known risks and open edges:

- stage 2 may require careful reset semantics across compaction and loaded conversations, which affects both `RunState` lifecycle and masking tests
- stage 3 touches prompt composition, tool registration, scratchpad persistence, and post-turn handling, so regression risk is broader than the other stages
- stage 3 has a real recursion hazard if pivot-turn inference reuses the normal assembled-request path and thereby injects scratchpad state into the scratchpad-inference request itself

## Verification Strategy

### Sources
- `AGENTS.md`
- `README.md`
- `Makefile`
- `internal/agent/context_management_integration_test.go`
- `internal/agent/file_tracker_test.go`
- `internal/prompt/masking_test.go`
- `internal/config/validate_test.go`

### Defaults
- execution_verification_timing: step_or_stage_exceptions_only
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### gofmt
- preferred_mode: fix
- fix:
  - `gofmt -w <changed files>`
- check:
  - `gofmt -d $(git ls-files '*.go')`
- use_check_only_when:
  - validating repo-wide formatting without mutating unrelated files
  - reviewer wants confirmation after implementation without introducing formatting churn

#### targeted_go_tests
- preferred_mode: check
- fix:
  - none
- check:
  - `go test ./internal/agent -run TestFileTracker`
  - `go test ./internal/agent -run TestContext`
  - `go test ./internal/prompt -run TestMask`
  - `go test ./internal/config -run Test`
- use_check_only_when:
  - always, because tests have no safe fix mode

#### package_go_tests
- preferred_mode: check
- fix:
  - none
- check:
  - `go test ./internal/agent ./internal/prompt ./internal/config`
- use_check_only_when:
  - always, because tests have no safe fix mode

#### repo_wide_tests
- preferred_mode: check
- fix:
  - none
- check:
  - `go test ./...`
  - `make test`
- use_check_only_when:
  - always, because tests have no safe fix mode
  - later in execution when stage-local verification has already passed

#### vet_and_build
- preferred_mode: check
- fix:
  - none
- check:
  - `go vet ./...`
  - `make build-binaries`
- use_check_only_when:
  - always, because these are validation/build commands only
  - after implementation is stable enough to justify repo-wide verification cost

### Tiers
- cheap:
  - gofmt
  - targeted_go_tests
- medium:
  - package_go_tests
- expensive:
  - repo_wide_tests
  - vet_and_build

### Required Boundaries
- step_level_exceptions:
  - `gofmt -w <changed files>` after each Go edit packet
  - run the most local affected test command before widening scope when changing file tracking, masking, or config validation behavior
- stage_level_exceptions:
  - after stage 1, run file-tracker and agent-context tests covering annotation behavior
  - after stage 2, run prompt masking tests plus affected agent-context tests
  - after stage 3, run scratchpad/config/context integration tests before broad repo checks
- end_of_implementation:
  - repo_wide_tests
  - vet_and_build
- reviewer_after_fix:
  - rerun the smallest failed or affected check first
  - rerun broader repo-wide checks only after targeted checks pass

### Assumptions
- `go test ./internal/agent -run ...`, `go test ./internal/prompt -run ...`, and `go test ./internal/config -run ...` are the right minimal checks because the relevant tests already exist beside the affected code
- repo policy prefers `gofmt -w` on changed files during execution, with repo-wide formatting checks deferred to broader validation
- no separate linter beyond `go vet` is currently repo-mandated

### Uncertainties
- exact test names for stage 2 and stage 3 will need to be confirmed when the detailed execution steps are written
- if stage 3 introduces a new provider-call path for pivot inference, additional provider-focused tests may be needed beyond the current context-management suites

## Decision Log

- Research skipped because this is a repo-internal planning task driven by a local design document and current code, not an external or time-sensitive dependency question.
- Planning directory chosen as `.project_planning/2026-05-02_context-management-implementation/` to satisfy the dated planning-bundle convention and give a stable branch slug later.
- Overview keeps the source plan's three stages because stage 1 is a correctness prerequisite and stages 2 and 3 are separable after that.
- Verification defaults to local-package tests first, broad repo validation late, consistent with `AGENTS.md` and the repo `Makefile`.
