# Stage 8 — Delegation Scaffolding

## Request

Implement the Stage 8 "Delegation scaffolding" section of `docs/ROADMAP.md` and `docs/INITIAL_IMPLEMENTATION_PLAN.md`. Build the seams that let the main (parent) agent spawn isolated sub-agents, without shipping advanced delegation features yet.

User-clarified architecture:
- A model-facing tool named `delegate` is exposed to the parent agent. Parent decides when to spawn.
- Sub-agents run with fresh context: no parent transcript leak, own system prompt, own tool registry.
- Sub-agents cannot themselves spawn further sub-agents (no nesting). Enforced at executor/tool-registry level; if a child invokes `delegate`, it receives a clean tool error.
- Concurrency of sub-agents is capped by `provider.parallelism` through the central scheduler. Parallelism is achieved by the parent emitting multiple `delegate` tool_use blocks in a single assistant turn.
- Stage 8 ships **one-shot, synchronous** delegation only. Re-promptable sessions and background mode are deferred (tracked in `docs/IDEAS.md`).
- Resource limits follow config defaults; `delegate` tool args may tighten but not loosen them.
- Model override: any model configured in the user's config is fair game for sub-agents (no tier gating).
- On over-long child output, the child is asked to run one additional summarisation turn before returning, rather than hard-truncating.
- `touched_files` in the result envelope is deferred to `docs/IDEAS.md`.

## Overview

### Scope

New `internal/delegation/` package with:
- `contract.go` — `DelegationSpec` (parent → child) and `DelegationResult` (child → parent) structs, plus status enum.
- `task.go` — one-shot `SpawnDelegate(ctx, spec) (DelegationResult, error)` entrypoint; contract shaped so a future `SessionHandle` / re-promptable variant can be added without breaking changes.
- `result.go` — result construction, output-size detection, summarisation-turn trigger, status classification.
- `scaffold.go` — builds the scoped context handoff (fresh system prompt, project AGENTS.md through normal flow, no parent transcript); constructs child tool registry with `delegate` filtered out.
- `limits.go` — default config caps (max_turns, timeout, output_limit_tokens); tighten-only enforcement against tool args.

New `internal/agent/subagent_state.go`:
- Isolated agent state type suitable for driving a child run through existing loop machinery. Distinct from parent state; shares no mutable references.

Extend `internal/provider/scheduler.go`:
- Ensure every LLM call (parent and child) acquires a slot from a single shared scheduler bounded by `provider.parallelism`. Confirm no bypass path exists.

Extend `internal/output/log.go`:
- New event types `EventDelegationStarted`, `EventDelegationComplete`, `EventDelegationFailed` with typed payloads carrying `agent_id`, task preview, status, turn/token counters, and error strings.

Extend `internal/tui/content.go`:
- Handle the new delegation events as muted placeholder blocks. No dedicated UI polish.

Tool surface:
- Register a `delegate` tool in the parent's tool registry. Schema includes `task` (required), `context`, `system_prompt`, `model`, `max_turns`, `timeout`. The child's tool registry excludes `delegate`.

Tests (per Stage 8 plan):
- Unit: contract serialisation, child-state isolation from parent, allowed-tools filtering, limit inheritance + tighten-only override, nesting-block behaviour.
- Integration: drive a child run end-to-end behind the internal interface (no model-facing exposure required for the integration test to pass — direct Go-level entry is sufficient), scheduler enforces `parallelism` across parent + child instances, delegation events flow through the event interface and land in the TUI content pane.

### Out of scope

- Re-promptable child sessions.
- Background / non-blocking delegation (`background: true`).
- `touched_files` result metadata.
- Delegation-specific TUI design beyond muted placeholder blocks.
- Skill-scoped or permission-scoped sub-agent definitions.
- Tier-based model gating.

### Key non-obvious decisions

- `delegate` **is** a model-facing tool from day one (Stage 8 docs read as "no full delegated execution", but a tool seam with an internally-driven child run is required to match the rest of the architecture). The parent can call it; the child cannot.
- Stage 8 exit criteria require that sub-agent execution can later be added without refactoring the loop architecture — so the child lifecycle goes through the normal agent loop machinery, gated by the scheduler.
- Summarisation-over-truncation on oversized output means the result pipeline needs a one-extra-turn escape hatch inside the child before returning.

## Verification Strategy

### Sources
- `AGENTS.md` (root) — authoritative list of build/test/verify commands.
- `/home/luis/.claude/CLAUDE.md` via the auto-loaded CLAUDE.md — mirrors AGENTS.md for this repo.
- `Makefile` — only defines `build-binaries`; no lint/test targets.
- No `.github/workflows` present at time of planning; CI policy does not constrain local verification.

### Defaults
- execution_verification_timing: step_or_stage_exceptions_only
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: false

### Commands

#### gofmt
- preferred_mode: fix
- fix:
  - `gofmt -w <changed Go files>`
- check:
  - `gofmt -l <changed Go files>`
- use_check_only_when:
  - reviewer is verifying without permission to mutate files

#### go_vet
- preferred_mode: check
- fix:
  - (none; `go vet` has no fix mode)
- check:
  - `go vet ./internal/delegation/... ./internal/agent/... ./internal/provider/... ./internal/output/... ./internal/tui/...`
  - `go vet ./...` (end of implementation)
- use_check_only_when:
  - always

#### go_build
- preferred_mode: check
- fix:
  - (none)
- check:
  - `go build ./...`
- use_check_only_when:
  - always

#### go_test_targeted
- preferred_mode: check
- fix:
  - (none)
- check:
  - `go test ./internal/delegation/...`
  - `go test ./internal/agent/...`
  - `go test ./internal/provider/...`
  - `go test ./internal/output/...`
  - `go test ./internal/tui/...`
- use_check_only_when:
  - always

#### go_test_full
- preferred_mode: check
- fix:
  - (none)
- check:
  - `go test ./...`
- use_check_only_when:
  - end of implementation, or when cross-package risk warrants it

#### make_build_binaries
- preferred_mode: check
- fix:
  - (none)
- check:
  - `make build-binaries`
- use_check_only_when:
  - end of implementation (validates both `cmd/steiner` and `cmd/steiner-core-tools` compile and link)

### Tiers
- cheap:
  - gofmt
  - go_vet
  - go_build
- medium:
  - go_test_targeted
- expensive:
  - go_test_full
  - make_build_binaries

### Required Boundaries
- step_level_exceptions:
  - after any Go edit, run `gofmt -w <changed files>` before marking the step complete (AGENTS.md rule)
- stage_level_exceptions:
  - none
- end_of_implementation:
  - gofmt
  - go_vet
  - go_build
  - go_test_full
  - make_build_binaries
- reviewer_after_fix:
  - rerun `go vet` and `go test` for the specific packages whose files changed
  - rerun `go test ./...` only if the fix touched shared interfaces (scheduler, event types, agent state)

### Assumptions
- No lint tool (golangci-lint, staticcheck) is mandated by the repo beyond `go vet`. `AGENTS.md` does not reference one.
- There is no dedicated integration-test target; integration tests live inside the same `go test` invocations via package tests and `testdata/` fixtures.

### Uncertainties
- Whether `make build-binaries` needs to run on every step or only at end-of-implementation — defaulting to end-of-implementation since `go build ./...` already validates compilation for both binaries' transitive packages.

## Decision Log

- **One-shot, synchronous delegation for Stage 8.** Re-promptable sessions and background mode land in a later stage; background + its re-prompt pairing are captured in `docs/IDEAS.md`. Reason: scaffolding scope; matches dominant cross-framework pattern; keeps child lifecycle simple; concurrency still possible via multi-tool-call emission.
- **`delegate` tool is model-facing.** User architecture requires a tool; child lifecycle routed through normal agent loop so future upgrades need no redesign.
- **No nesting, enforced at tool-registry level.** Child executor receives a tool registry with `delegate` omitted; child attempting to spawn receives a clean tool error rather than session kill.
- **Any configured model fair game for sub-agents.** Tier gating dropped to keep the config story simple; the model cannot know relative cost.
- **Over-length child output → summarisation turn, not hard truncation.** Truncation is unpredictably lossy; a single summarisation turn preserves semantics at a bounded cost.
- **`touched_files` deferred to `docs/IDEAS.md`.** No established framework returns this; adding it couples the delegation contract to tool internals before the tool registry is stable.
- **Fresh context per child.** No parent transcript, no parent memory injection; project `AGENTS.md` reaches the child through the normal context gathering path, not via a direct handoff.
- **Scheduler is the single gate for LLM concurrency.** Parent and all children share one `provider.parallelism`-bounded budget; no bypass path.
- **Branch name.** `cl/2026-04-23_stage8_delegation_scaffolding`.
