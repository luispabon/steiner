## Scope Reviewed

- Planning folder: `.project_planning/2026-04-23_stage8_delegation_scaffolding`
- Branch: `cl/2026-04-23_stage8_delegation_scaffolding`
- Review pass: initial reviewer pass
- Files inspected:
  - `internal/delegation/contract.go`
  - `internal/delegation/limits.go`
  - `internal/delegation/result.go`
  - `internal/delegation/scaffold.go`
  - `internal/delegation/task.go`
  - `internal/delegation/tool.go`
  - `internal/delegation/integration_test.go`
  - `internal/output/log.go`
  - `internal/tui/content.go`
  - `internal/tool/executor.go`

## Inputs Reviewed

- `overview.md`
- `plan.yaml`
- `execution.md`
- `research.md`
- Repository diff on `cl/2026-04-23_stage8_delegation_scaffolding`

## Findings

### Blocking

#### R1: Oversized-output summarisation turn is not given the oversized answer to summarise

- Severity: `blocking`
- Evidence:
  - `internal/delegation/task.go:56-72` builds the follow-up summarisation run from `req.Prompt.Conversation`.
  - The appended prompt is only `"Your previous response was too long. Please provide a concise summary."`
  - The oversized assistant response from the first child run is never appended to the summarisation conversation, so the second run is asked to summarise content it cannot see.
- Why this blocks handoff:
  - The approved Stage 8 contract explicitly requires an additional summarisation turn "inside the child" when output exceeds `output_limit_tokens`.
  - As implemented, the summarisation turn can only generate a new answer from the original task, not a summary of the oversized output that triggered it.
- Notes:
  - `internal/delegation/integration_test.go` only asserts that a second provider call happened and that `result.Summary == "short summary"`; it does not assert that the second prompt included the original oversized output.

#### R2: Oversized child results still return the full unbounded `Output` to the parent

- Severity: `blocking`
- Evidence:
  - `internal/delegation/task.go:52-80` computes `result := BuildResult(...)`, detects oversize, and only populates `result.Summary`.
  - `result.Output` is never replaced, truncated, or otherwise bounded before the tool result is returned.
  - The planning artifacts require `output_limit_tokens` to be enforced before returning to the parent, and the stage-3 acceptance says "the returned output respects the limit."
- Why this blocks handoff:
  - The current result envelope still injects the oversized child answer into the parent tool result, defeating the Stage 8 context-bloat protection the summarisation path was meant to provide.

#### R3: Child runs do not actually receive an inherited tool surface, only a no-op executor

- Severity: `blocking`
- Evidence:
  - `internal/delegation/scaffold.go:64-104` accepts `childReg` but sets `req.Tools = nil` and `req.Executor = &noopExecutor{reg: childReg}`.
  - `noopExecutor.Execute` in `internal/delegation/scaffold.go:56-62` rejects every tool call, not just `delegate`.
  - The approved request says sub-agents get their own tool registry and cannot nest because `delegate` is filtered out at the executor/tool-registry layer.
- Why this blocks handoff:
  - The implementation currently disables all child tool use rather than giving children an isolated registry with `delegate` removed.
  - That is a scope mismatch against the requested architecture, not just a deferred enhancement.

#### R4: The `timeout` delegate limit is parsed and stored but never enforced

- Severity: `blocking`
- Evidence:
  - `internal/delegation/tool.go:65-67` parses the `timeout` input into `overrides.Timeout`.
  - `internal/delegation/limits.go:44-47` preserves the tightened timeout in `DelegationLimits`.
  - `internal/delegation/task.go:32-88` calls `runner.Run(ctx, req)` directly and never derives a timed context from `spec.Limits.Timeout`.
  - `agent.RunRequest` has no timeout field; timeout enforcement must therefore happen via context wrapping before the run starts.
- Why this blocks handoff:
  - Stage 8 exposes `timeout` on the tool schema and includes it in the limits model, so accepting it without enforcing it is a behavioral contract violation.

## Fix Plan

- Proposed review-fix pass:
  - Fix `R1` by building the summarisation request from the completed child conversation or by explicitly appending the oversized assistant output before the follow-up prompt, so the second turn summarizes the actual prior answer.
  - Fix `R2` by enforcing `output_limit_tokens` on the returned result envelope: return bounded parent-visible output after the summarisation path, and only use truncation as the documented fallback when the summary itself still exceeds the limit.
  - Fix `R3` by wiring delegated children to a real isolated executor/tool surface derived from `childReg`, with `delegate` omitted but other allowed tools still exposed.
  - Fix `R4` by applying `spec.Limits.Timeout` through a derived context around child execution, including the summarisation retry path.
  - Add or tighten tests around:
    - summarisation request contents
    - bounded returned output after oversize handling
    - child access to non-`delegate` tools and clean rejection of `delegate`
    - timeout enforcement

## Fixes Applied

- None yet. Awaiting user approval for the reviewer fix plan.

## Verification

- Review evidence gathered from planning artifacts and repository state.
- If fix work is approved, rerun the smallest relevant checks first per `overview.md`:
  - targeted `go test` for `./internal/delegation/...`, `./internal/tool/...`, and any adjacent package touched by the approved fix pass
  - broaden to `go test ./...` only if the approved fix changes shared interfaces or requires wider confidence

## Final Status

- Review status: `fail`
- Blocking findings: `R1`, `R2`, `R3`, `R4`
- Non-blocking findings: none recorded in this pass
- Informational findings: none recorded in this pass
- Finaliser handoff: blocked pending approved review-fix pass
