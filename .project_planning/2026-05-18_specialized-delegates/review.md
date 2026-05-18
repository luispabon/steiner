# Review — Specialized Delegates

## Scope Reviewed

Feature branch `cl/2026-05-18_specialized-delegates` vs main. All files touched by the approved plan plus directly adjacent regression-risk areas.

## Inputs Reviewed

- `overview.md` — original request, architecture, design decisions, verification strategy
- `plan.yaml` — 4-stage implementation contract
- `execution.md` — step completions, verification runs, sub-agent log
- `research.md` — prior art survey
- Repository state: full diff from main, all new/changed files

Branch: `cl/2026-05-18_specialized-delegates`
Review status: **fail**

---

## Findings

### Blocking

#### B-1: Per-agent-type model resolution not wired

**Evidence:**
- Overview (lines 55-74) requires per-agent-type model selection via config aliases.
- Plan (stage-2-step-2 acceptance): "Handler resolves model from config, falls back to default."
- `AgentConfig.Model` field exists (`internal/config/config.go:170-173`) and is validated (`internal/config/validate_runtime.go:19-22`).
- `specialized_tools.go:82` always passes `deps.ResolvedModel` (parent model) to `BuildChildRun`. No code reads `SubAgentConfig.Agents[agentType].Model`.
- DELEGATION.md lines 139-151 document per-type model config with example YAML, but the code behind it is inert.
- Users setting `sub_agent.agents.explore.model: fast` would see no effect.

**Required fix:**
1. Add a model resolver dependency to `SpecializedToolDeps` (e.g. `ModelResolver func(alias string) (provider.Provider, provider.ResolvedModel, error)`).
2. In the handler, look up `deps.SubAgentCfg.Agents[string(agentType)].Model`.
3. If a model alias is configured, resolve it via the resolver to obtain a per-type provider and resolved model.
4. If no alias is configured or lookup returns empty, fall back to `deps.ResolvedModel` and `deps.Provider`.
5. Pass the resolved provider and model to `BuildChildRun`.
6. Add tests: configured alias resolves correctly, missing alias falls back, unknown alias returns error.
7. Resolve DELEGATION.md contradiction (constraint #5 "Single provider" vs per-type model config).

### Non-blocking

#### NB-1: Unrelated commit on feature branch

`d50c421 Replace thinking config with prompt suffix` is in the branch diff but not part of the specialized delegates feature. Affects: `cmd_model.go`, `commands_test.go`, `compaction.go`, `message_convert.go`, prompt suffix and thinking test files, config files. Should be noted for PR review — either rebase-split or acknowledged as intentional bundling.

#### NB-2: `validAgentTypes` duplication risk

`validate_runtime.go:10-16` manually mirrors `delegation.AllAgentTypes()` to avoid a circular import. Comment documents the intent. No cross-package test ensures the two lists stay in sync. If a new agent type is added to `AllAgentTypes()` but not to `validAgentTypes`, config validation would reject it. Low risk for v1 since the two files changed in the same step.

#### NB-3: DELEGATION.md internal contradiction

Constraint #5 (line 377): "Single provider: children use the same provider/model instance as the parent." Lines 139-151: documents per-type model config. These contradict each other. Both will need updating when B-1 is fixed.

### Informational

#### I-1: TUI enhancement not in plan

`a12f9d9` adds specialized delegate tool name rendering in TUI delegation boxes. Reasonable enhancement, correctly scoped.

#### I-2: `govulncheck` skipped

Execution.md notes `govulncheck` was skipped because not installed. Pre-existing condition, not a regression.

---

## Fix Plan

**Status: approved**

### B-1 fix: Per-agent-type model resolution

1. Add `ModelResolver func(alias string) (provider.Provider, provider.ResolvedModel, error)` to `SpecializedToolDeps`
2. In handler (`specialized_tools.go`), look up `deps.SubAgentCfg.Agents[string(agentType)].Model`; if set, resolve via `ModelResolver`; if empty, fall back to `deps.ResolvedModel` and `deps.Provider`
3. Supply resolver closure in `cmd/steiner/runner.go` `buildActiveRegistry()`
4. Fix DELEGATION.md constraint #5 — remove "Single provider" statement, update to reflect per-type model config reality
5. Add tests: alias resolves correctly, empty alias falls back, unknown alias errors

### NB-2 fix: Cross-package agent type sync test

Add a test in `cmd/steiner/runner_test.go` that verifies `config.validAgentTypes` (via validation) stays in sync with `delegation.AllAgentTypes()`.

### Files affected
- `internal/delegation/specialized_tools.go`
- `internal/delegation/specialized_tools_test.go`
- `cmd/steiner/runner.go`
- `cmd/steiner/runner_test.go`
- `docs/DELEGATION.md`

### Verification after fix
- `go test ./internal/delegation/...`
- `go test ./cmd/steiner/...`
- `go vet ./...`
- `gofmt -w` / `goimports -w` on changed files

---

## Fixes Applied

- Embedded `DelegateHandlerDeps` in `SpecializedToolDeps` and added a `ModelResolver` dependency for per-type aliases.
- Updated specialized delegate handlers to resolve configured `sub_agent.agents.<type>.model` aliases through `ModelResolver`, falling back to the parent provider/model when unset or when no resolver is available.
- Updated `cmd/steiner` registry wiring to build a resolver from `provider.Resolve(cfg, alias)` plus the runtime provider factory, and pass it into specialized tool definitions.
- Added/updated tests for configured alias resolution, missing config fallback, nil resolver fallback, resolver errors, and `delegation.AllAgentTypes()` validation sync.
- Updated `docs/DELEGATION.md` constraint #5 to describe default parent provider/model reuse plus per-type model alias overrides.

## Verification

- `gofmt -w internal/delegation/specialized_tools.go internal/delegation/specialized_tools_test.go cmd/steiner/runner.go cmd/steiner/runner_test.go`
- `goimports -w internal/delegation/specialized_tools.go internal/delegation/specialized_tools_test.go cmd/steiner/runner.go cmd/steiner/runner_test.go`
- `go test ./internal/delegation/... ./cmd/steiner/...`
- `go vet ./...`
- `go build ./...`
- `make quick-check`

## Final Status

**pass** — B-1 fixed and verification passed.
