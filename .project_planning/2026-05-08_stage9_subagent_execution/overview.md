## Request

Wire Stage 8's delegation infrastructure into the running steiner application so that the model can call a `delegate` tool to spawn synchronous sub-agents. Gate the feature behind `sub_agent.enabled`. Polish TUI rendering with spinner, task preview, compact result display, elapsed time, and token usage. Add integration tests proving end-to-end delegation, context isolation, and nesting prevention.

## Overview

### Current State

Stage 8 delivered a complete but disconnected delegation subsystem:

- **Contracts**: `DelegationSpec`, `DelegationResult`, `DelegationLimits` in `internal/delegation/contract.go`
- **Execution**: `SpawnDelegate` in `task.go`, `BuildChildRun` in `bootstrap.go`, child tool registry builder, tighten-only limit derivation
- **Tool definition**: `DelegateToolDef` returns a `tool.ToolDef` with schema; `NewDelegateHandler` creates the handler from `DelegateHandlerDeps`
- **Events**: `DelegationStarted`, `DelegationComplete`, `DelegationFailed` event types with TUI placeholder rendering
- **Tests**: Unit and integration tests covering contracts, bootstrap, limits, tool handler, child tool surface, oversized output summarisation

The gap: **nothing in `cmd/steiner/` imports `internal/delegation`**. The delegate tool is never registered. The model cannot see or call it.

### Architecture Decision: Per-Run Wiring

The delegate handler needs live references to the current provider, event sink, and registry. In interactive mode, the provider can change between runs (model switching). Therefore:

1. `cliRunner.Run()` creates the delegate handler fresh each run with current deps
2. The base registry from `cliRuntime` is cloned and the delegate tool is added to the clone
3. The augmented registry is used for both `Tools` (visible to model) and `Executor` construction
4. When `sub_agent.enabled` is false, no cloning or registration happens — zero overhead

This keeps the delegate tool lifecycle tied to the run, not the session.

### Wiring Flow

```
cliRunner.Run()
  ├─ check cfg.SubAgent.Enabled
  ├─ clone runtime.registry
  ├─ create delegate handler with DelegateHandlerDeps{
  │     Provider:    prov,
  │     ParentReg:   clonedRegistry,
  │     SubAgentCfg: cfg.SubAgent,
  │     Events:      events,
  │     Runner:      agent.NewRunner(),
  │     WorkDir:     workDir,
  │   }
  ├─ register DelegateToolDef(handler) on clonedRegistry
  ├─ build Executor from clonedRegistry
  └─ build RunRequest with clonedRegistry.ToProviderSpecs()
```

### TUI Rendering: Full Polish

Current delegation rendering is bare text ("delegate: starting child-X"). Stage 9 adds:

- **Start event**: muted inline block with task preview, bordered section header "⟩ delegate child-XXXX"
- **Active state**: animated spinner (dots cycling) with "running..." and elapsed time, updating via tick messages
- **Complete event**: compact result block showing status, turn count, token count, elapsed time, and truncated output summary
- **Failed event**: error block with agent ID, task preview, and error message in warning/error style
- **Collapsible detail**: result output expandable/collapsible (toggle keybind)

Implementation approach:
- Track active delegation state in content buffer (agentID → start time)
- Use Bubble Tea tick commands for spinner animation during active delegation
- Render delegation blocks using theme's muted + border styles
- Add `DelegationActiveEvent` or use existing events with timing enrichment

### Scope Boundaries

**In scope:**
- Delegate tool wiring in `cmd/steiner/runner.go`
- Config gating (`sub_agent.enabled`)
- TUI delegation rendering (spinner, compact result, elapsed time, token usage, collapsible output)
- Integration tests: end-to-end delegation, nesting prevention, parent context size, config gating
- Registry cloning support in `tool.Registry`

**Out of scope (per PRD stage boundaries):**
- Parallel sub-agents
- Background (non-blocking) delegation
- Nested sub-agents
- Re-promptable child sessions
- `touched_files` in result envelope
- Shared memory between agents

### Key Design Decisions

1. **Registry cloning**: `tool.Registry` needs a `Clone()` method to avoid mutating the shared base registry when adding the delegate tool per-run
2. **Child runner**: Uses `agent.NewRunner()` directly — same runner type as parent, satisfies `AgentRunner` interface
3. **Event routing**: Child events flow through the same event sink as parent, so TUI sees delegation lifecycle events in real time
4. **Approval bypass**: Child tools auto-approved (already set in `bootstrap_support.go` with `ApprovalModeAuto`)
5. **Spinner lifecycle**: TUI tracks active delegations by agentID; spinner tick starts on `DelegationStarted`, stops on `Complete`/`Failed`

### Files to Change

| File | Change |
|------|--------|
| `internal/tool/registry.go` | Add `Clone()` method |
| `cmd/steiner/runner.go` | Wire delegate tool per-run when enabled |
| `cmd/steiner/tools.go` | No change needed (base registry stays clean) |
| `internal/tui/content.go` | Enhanced delegation event formatting, collapsible output |
| `internal/tui/content_events.go` | Delegation block rendering with spinner state |
| `internal/tui/model.go` | Tick command for spinner, active delegation tracking |
| `internal/tui/keys.go` | Keybind for expanding/collapsing delegation output |
| `internal/delegation/tool.go` | Minor: export `DelegateToolName` constant |
| `internal/tool/registry_test.go` | Tests for Clone() |
| `cmd/steiner/runner_test.go` | Tests for delegate tool wiring |
| `internal/delegation/integration_test.go` | End-to-end wiring tests |
| `internal/tui/content_test.go` | TUI delegation rendering tests |

### Risks

1. **Provider lifetime**: If the provider is closed/recycled between the parent creating the handler and the child using it, child calls will fail. Mitigation: provider factory creates fresh instances; child run completes synchronously before parent run returns.
2. **Event sink contention**: Parent and child emit to the same sink concurrently (child runs on same goroutine synchronously, so actually sequential). Low risk.
3. **Registry mutation**: Without Clone(), adding delegate tool would mutate the shared registry visible to other runs. Mitigated by adding Clone().

## Verification Strategy

### Sources
- CLAUDE.md (work loop section)
- Makefile (build-binaries, test, check, format targets)

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### format
- preferred_mode: fix
- fix:
  - `gofmt -w <changed-files>`
- check:
  - `gofmt -d <changed-files>`
- use_check_only_when:
  - reviewer pass

#### vet
- preferred_mode: check
- check:
  - `go vet ./...`

#### unit-test-targeted
- preferred_mode: check
- check:
  - `go test ./path/to/pkg -run TestName`

#### unit-test-broad
- preferred_mode: check
- check:
  - `go test ./...`

#### build
- preferred_mode: check
- check:
  - `go build ./...`
  - `make build-binaries`

### Tiers
- cheap:
  - format
  - vet
- medium:
  - unit-test-targeted
  - build
- expensive:
  - unit-test-broad

### Required Boundaries
- step_level_exceptions:
  - run `gofmt -w` on changed files after each step
  - run targeted tests for changed packages after each step
- stage_level_exceptions:
  - none
- end_of_implementation:
  - format
  - vet
  - unit-test-broad
  - build
- reviewer_after_fix:
  - run targeted tests for any package touched by fixes
  - run `go vet ./...` after all fixes applied

### Assumptions
- No CI pipeline to verify against (no .github/workflows found)
- `go test ./...` covers all test packages including delegation and TUI

### Uncertainties
- None significant

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | Per-run delegate handler creation in `cliRunner.Run()` | Provider and events are fresh per-run; avoids stale references in interactive mode with model switching |
| 2 | Registry `Clone()` method instead of rebuilding | Cheaper than rebuilding full registry; avoids mutating shared base |
| 3 | Full TUI polish (spinner, elapsed time, collapsible output) | User chose full polish over functional minimum |
| 4 | Child events routed through parent's event sink | Simplest approach; child runs synchronously so no concurrency issues; TUI already handles delegation event types |
| 5 | Export `DelegateToolName` from delegation package | Needed for wiring in `cmd/steiner/` without hardcoding string |
| 6 | No changes to delegation contracts or bootstrap | Stage 8 contracts are well-tested and sufficient |
