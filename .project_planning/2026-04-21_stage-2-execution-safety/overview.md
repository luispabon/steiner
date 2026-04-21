## Request

Plan the implementation of Stage 2, "Execution safety and safer mutation", using only the Stage 2 sections from `docs/ROADMAP.md` and `docs/INITIAL_IMPLEMENTATION_PLAN.md` as planning inputs from those docs.

Constraints and expectations:

- Preserve package boundaries defined in `AGENTS.md`
- Treat Stage 2 as the active scope, not broader roadmap work
- Prefer `edit` over `patch` as the first safer mutation primitive
- Keep `write` available, but stop relying on blind overwrite as the only mutation path
- Improve execution safety through policy and bounded output handling before delegation work

## Overview

Stage 2 should be implemented as a focused safety layer across tool execution, core tool behavior, and approval UX, without introducing new architectural coupling between `internal/agent`, `internal/tool`, and the core tools binary.

The work should center on five coherent outcomes:

1. Add a tool-execution path policy in `internal/tool` that enforces project-root confinement, blocked-path rejection, writable-path allowlists, and safe `cwd` handling for shell execution.
2. Introduce bounded tool-result capture and normalization so subprocess stdout/stderr are size-limited, truncation is explicit, and likely binary output is detected and represented safely.
3. Add a first safer mutation primitive, `edit`, to `cmd/steiner-core-tools/`, using exact old/new replacement semantics that are easy to validate and preview.
4. Improve approval previews so prompt-mode tools expose enough information for informed approval decisions, especially path, cwd, timeout, and replacement or diff excerpts.
5. Extend tests at unit and integration layers to lock in path safety, truncation behavior, binary handling, `edit` correctness, and approval-preview content.

Planning structure decision:

- Approval preview rendering will be folded into the executor/output safety work rather than planned as a standalone implementation step.
- A separate implementation step is still expected for the new `edit` mutation primitive and its schema/registry/test wiring.

Likely implementation areas based on the current repository state:

- `internal/tool/executor.go`
- `internal/tool/approval.go`
- `internal/tool/types.go`
- `internal/tool/schema.go`
- `cmd/steiner-core-tools/bash.go`
- `cmd/steiner-core-tools/write.go`
- new Stage 2 files such as:
  - `internal/tool/policy.go`
  - `internal/tool/output.go`
  - `internal/tool/preview.go`
  - `internal/agent/tool_result.go`
  - `cmd/steiner-core-tools/edit.go`

Current code confirms the main gaps this stage needs to close:

- `bash` currently resolves and accepts absolute `cwd` values, so confinement is not yet enforced.
- `write` performs direct overwrite with no safer targeted-edit path.
- tool subprocess execution currently captures full stdout/stderr in memory and error payloads, with no truncation metadata.
- approval handling resolves whether a prompt is needed, but does not yet prepare richer preview material for the approver.

Planning assumptions:

- Path-policy decisions should be centralized in `internal/tool`, not duplicated independently in each tool implementation.
- The output-capping and binary-detection behavior should shape both success and failure envelopes so prompt assembly can remain bounded later.
- The `edit` primitive should be exact-match replacement only for this stage; patch application should remain out of scope.
- Approval preview generation should be derived from structured tool input and normalized result metadata rather than ad hoc string formatting spread across the codebase.

Primary risks:

- scattering safety checks between executor, core tools, and agent code in a way that weakens policy consistency
- allowing shell execution outside project root through incomplete `cwd` normalization or symlink/path-traversal edge cases
- truncating subprocess output but still passing oversized raw payloads into agent-visible errors
- designing approval previews around raw text blobs instead of stable structured preview metadata

## Verification Strategy

### Sources
- `AGENTS.md`
- `README.md`
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
  - `gofmt -w <touched-go-files>`
- check:
  - `gofmt -d <touched-go-files>`
- use_check_only_when:
  - when validating formatting drift without changing files during review
  - when untouched generated or external files would create out-of-scope churn

#### vet
- preferred_mode: check
- fix:
  - none
- check:
  - `go vet ./...`
- use_check_only_when:
  - always; `go vet` is check-only

#### unit-and-integration-tests
- preferred_mode: check
- fix:
  - none
- check:
  - `go test ./...`
- use_check_only_when:
  - always; test execution is check-only

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
  - vet
  - build
- expensive:
  - unit-and-integration-tests

### Required Boundaries
- step_level_exceptions:
  - none
- stage_level_exceptions:
  - none
- end_of_implementation:
  - formatting
  - vet
  - build
  - unit-and-integration-tests
- reviewer_after_fix:
  - rerun the minimal relevant targeted checks first when a fix is localized
  - rerun `go test ./...` before final handoff if execution-safety changes affect shared tool/runtime paths

### Assumptions
- `AGENTS.md` is the authoritative source for requiring `gofmt` and `go vet` before commit.
- `README.md` is the best available evidence for `go build ./...` and `go test ./...` in the absence of CI or task-runner config.
- repo-wide formatting on touched Go files is acceptable because this is a Go-only repository with no formatter config indicating a narrower policy.

### Uncertainties
- no CI configuration or dedicated lint runner was found in the shallow verification discovery pass
- no narrower targeted test command surface is documented yet, so test scoping will likely need to be chosen from impacted packages during execution

## Decision Log

- Research was not required because this is a repo-internal Go planning task with explicit Stage 2 scope and no external dependency that would materially change the plan.
- The planning focus remains limited to the Stage 2 sections of `docs/ROADMAP.md` and `docs/INITIAL_IMPLEMENTATION_PLAN.md`.
- `edit` is the recommended safer mutation primitive for this stage; `patch` is intentionally excluded from the initial execution plan.
- Approval preview rendering will be planned together with executor/output safety because it depends directly on the same normalized metadata and policy-owned execution details.
- Verification discovery was kept shallow and evidence-driven per planner rules; no broader repository sweep was performed.
