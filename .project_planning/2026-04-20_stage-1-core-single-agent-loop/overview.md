## Request

Plan the implementation work for `## Stage 1 - Core single-agent loop`, using only the corresponding Stage 1 sections from `docs/ROADMAP.md` and `docs/INITIAL_IMPLEMENTATION_PLAN.md` as the primary scope anchors.

Key outcomes in scope:

- ship the thinnest useful single-agent that can complete small end-to-end tasks
- add an OpenAI-compatible provider implementation
- implement the single-agent ReAct loop with sequential tool execution
- support REPL mode and `--exec` mode
- add streaming output where supported
- add core termination controls:
  - max turns
  - model call cancellation
  - tool timeout handling
- load a minimal system preamble
- load `AGENTS.md`
- add bounded project context loading
- support skills discovery and explicit invocation
- implement the core tools:
  - `read`
  - `glob`
  - `search`
  - `write`
  - `bash`
- add an approval system
- add plain logging of model calls, tool calls, and stop reasons

Out of scope for this stage:

- sub-agents
- compaction
- advanced edit primitives
- persistence
- concurrency beyond scheduler enforcement

## Overview

Stage 1 should be planned as the first end-to-end usable slice of the product, built on top of the Stage 0 foundation implied by the roadmap and implementation-plan documents. The plan should optimize for a thin vertical path from CLI entrypoint to provider call to tool execution to observable completion, not for broad feature coverage or polished UX.

The repository currently contains planning documents but no visible implementation tree, root project manifest, CI configuration, or repository-level verification runner. That means the Stage 1 plan should assume some Stage 0 scaffolding either already exists on the execution branch by the time implementation begins or must be treated as a dependency boundary rather than silently recreated inside Stage 1. The detailed plan should therefore:

- treat provider, prompt assembly, tool execution, agent loop, approvals, `--exec`, REPL, and logging as the owned Stage 1 surfaces
- preserve strictly sequential tool execution and deterministic project-root-relative path handling
- keep context loading hard-bounded by an explicit budget rather than best effort
- avoid designing `write` in a way that blocks a future richer edit primitive
- keep skills explicit and user-invoked rather than injecting them as peer system authority
- aim acceptance around the documented Stage 1 exit criteria:
  - fix a small bug in a toy repo
  - read files, edit one file, run a targeted test, and explain the result
  - enforce approvals correctly
  - keep path handling deterministic
  - behave predictably with `parallelism: 1` on constrained hardware

Likely implementation areas, based on the Stage 1 breakdown in `docs/INITIAL_IMPLEMENTATION_PLAN.md`, are:

- `internal/provider`
- `internal/tool`
- `internal/prompt`
- `internal/agent`
- `internal/skill`
- `internal/repl`
- CLI/config wiring
- Stage 1-focused tests and test fixtures

Primary planning risks are:

- overbuilding terminal UX before the loop is reliable
- letting project context or future skill metadata dominate prompt size
- coupling provider-specific streaming/tool-call behavior directly into the loop instead of normalizing through a shared response model
- failing to define clear approval and timeout boundaries early enough for predictable operator control

## Verification Strategy

### Sources
- `AGENTS.md` at repository root: present but empty
- `README.md` at repository root: present but empty
- repository root file inventory: no root project manifest, task runner, CI config, or source tree currently present
- `.opencode/package.json`: local plugin dependency manifest only, with no project scripts or verification commands

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: false

### Commands

#### formatting
- preferred_mode: check
- fix:
  - none discovered
- check:
  - none discovered
- use_check_only_when:
  - the repository still lacks a formatter configuration or standard formatter command
  - broad formatting would create speculative or out-of-scope churn

#### lint
- preferred_mode: check
- fix:
  - none discovered
- check:
  - none discovered
- use_check_only_when:
  - the repository has not yet introduced a lint runner

#### unit-tests
- preferred_mode: check
- fix:
  - none discovered
- check:
  - none discovered
- use_check_only_when:
  - Stage 1 implementation has not yet established a runnable unit test command

#### integration-tests
- preferred_mode: check
- fix:
  - none discovered
- check:
  - none discovered
- use_check_only_when:
  - integration fixtures and a test runner are not yet available

#### build
- preferred_mode: check
- fix:
  - none discovered
- check:
  - none discovered
- use_check_only_when:
  - the repository has not yet established a build or compile validation command

### Tiers
- cheap:
  - formatting
  - lint
- medium:
  - unit-tests
  - build
- expensive:
  - integration-tests

### Required Boundaries
- step_level_exceptions:
  - add step-specific verification commands only after the implementation branch exposes concrete manifests or runners
- stage_level_exceptions:
  - none
- end_of_implementation:
  - formatting
  - lint
  - unit-tests
  - build
  - integration-tests
- reviewer_after_fix:
  - rerun the smallest relevant implemented checks first
  - do not invent repo-wide checks that are still absent from the codebase

### Assumptions
- Stage 1 execution will occur after Stage 0 has supplied the minimal scaffolding the Stage 1 components depend on
- language-specific formatter, build, and test commands will be discoverable from manifests introduced by implementation work or already present on the execution branch
- verification should stay targeted until the repository exposes an explicit project-wide validation surface

### Uncertainties
- the implementation language and concrete runner commands are not discoverable from the current repository state
- it is not yet clear whether Stage 1 should include creation of missing manifests and test harnesses or assume they already exist from Stage 0
- CI expectations are unknown because no CI configuration is present in the current tree

## Decision Log

- Research was explicitly skipped after confirming the request is repo-local and well-scoped by existing Stage 1 documents.
- The file named in the prompt as `docs/IMPLEMENTATION_PLAN.md` does not exist; `docs/INITIAL_IMPLEMENTATION_PLAN.md` was used as the matching implementation-plan source.
- Verification strategy is intentionally conservative because the repository currently exposes no implementation manifests, no source tree, and no CI or task-runner commands.
- The overview treats missing implementation scaffolding as a dependency/risk to be surfaced in planning, not something to silently absorb into Stage 1 scope without approval.
