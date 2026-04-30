# Architectural Review Subject 5

## Subject

Deepen the **Tool Execution Pipeline** module in `internal/tool`.

## Summary

Refactor tool execution so one deep module owns the full tool-invocation flow with explicit internal phases. Today, `internal/tool/executor.go` mixes tool lookup, input normalization, path validation, approval policy, approval prompting, handler/subprocess dispatch, output decoding, and structured error shaping in one long execution path.

The target state is:

- `Executor.Execute(...)` remains the stable caller-facing seam initially.
- A concrete **Tool Execution Pipeline** module owns the internal execution phases:
  - resolve tool definition
  - normalize and validate input
  - resolve approval mode and preview
  - execute approval flow if required
  - dispatch to handler or subprocess
  - decode and validate output
  - shape final result or structured execution error
- Handler-based and subprocess-based tools remain under one pipeline unless a later subject proves they need to diverge.

This subject should be implemented incrementally and merged stage by stage.

## Current friction

Current logic is concentrated in:

- [`internal/tool/executor.go`](/home/luis/Projects/AI/steiner/internal/tool/executor.go:1)

Specific friction points:

- `Execute(...)` performs almost the entire execution pipeline inline ([executor.go:52](/home/luis/Projects/AI/steiner/internal/tool/executor.go:52)).
- Input normalization and path policy enforcement happen early, but the phase boundary is implicit ([executor.go:62](/home/luis/Projects/AI/steiner/internal/tool/executor.go:62)).
- Approval mode resolution, preview construction, prompt/deny behavior, and approval response handling all live inside the same function ([executor.go:71](/home/luis/Projects/AI/steiner/internal/tool/executor.go:71)).
- Handler-based execution and subprocess-based execution diverge inline rather than through a clear internal phase boundary ([executor.go:130](/home/luis/Projects/AI/steiner/internal/tool/executor.go:130)).
- JSON envelope decoding and error shaping are part of the same long control flow as subprocess launch ([executor.go:168](/home/luis/Projects/AI/steiner/internal/tool/executor.go:168)).

The current code is understandable, but the real interface is the end-to-end execution pipeline, and that interface is not explicit enough.

## Invariants to preserve

- `Executor.Execute(ctx, toolName, input)` should remain the caller-facing seam initially.
- Path policy validation must continue to happen before execution.
- Approval policy must continue to support:
  - auto
  - prompt
  - denied
- Approval previews must continue to be computed before approval prompting.
- Handler-backed tools must still bypass subprocess execution.
- Subprocess-backed tools must still:
  - marshal normalized JSON input
  - honor per-tool timeouts
  - capture bounded stdout/stderr
  - decode the JSON envelope
- Structured execution errors must remain informative and include output metadata where relevant.
- Package boundaries from `AGENTS.md` must remain intact:
  - `internal/tool` owns registry, policy, executor, and output shaping
  - per-tool business logic remains in handlers or tool executables

## Design decisions locked for execution

These decisions are settled for this subject and should not be re-litigated during implementation unless a concrete blocker appears.

### Decision 1: use a concrete pipeline module

Chosen direction:

- Use a concrete internal execution pipeline, not a formal interface hierarchy.

Reason:

- The architecture problem is implicit phases, not missing substitutability.
- There is only one real execution pipeline today.

Implication for implementation:

- Prefer private concrete helpers or a private concrete pipeline type.
- Avoid exported phase interfaces.

### Decision 2: keep `Executor.Execute(...)` stable initially

Chosen direction:

- Preserve `Executor.Execute(ctx, toolName, input)` as the external seam for this subject.

Reason:

- This limits churn into callers such as the agent loop and delegation code.
- The subject is about internal deepening first.

Implication for implementation:

- Internal refactoring should happen behind the existing entry point.

### Decision 3: introduce explicit internal phases

Chosen direction:

- Make the execution phases explicit inside the module.

Reason:

- The missing module is the pipeline itself.
- Clear phase boundaries improve locality and testability.

Implication for implementation:

- Avoid a refactor that only extracts a few arbitrary helpers without clarifying the phases.

### Decision 4: keep handler and subprocess paths under one pipeline

Chosen direction:

- Handler-backed and subprocess-backed tools remain part of the same **Tool Execution Pipeline**.

Reason:

- They still share most of the flow: definition lookup, normalization, approval, and output/result shaping concerns.
- Splitting them now would likely be premature.

Implication for implementation:

- Internal branching is fine.
- A separate architectural subject for handler vs subprocess execution is out of scope here.

### Decision 5: re-center tests on pipeline behavior

Chosen direction:

- Add tests that target pipeline phases and outcomes directly, while preserving end-to-end coverage.

Reason:

- The real seam is not just “did this tool run,” but “how did the pipeline handle normalization, approval, execution, and decoding.”

Implication for implementation:

- Keep end-to-end tests.
- Add more targeted tests around phase outcomes and structured errors.

## Proposed target design

Keep the public `Executor` type, but deepen its internals around a concrete execution pipeline.

Illustrative shape:

```go
package tool

import "context"

type executionInput struct {
	ToolName string
	Input    map[string]any
}

type executionContext struct {
	Def             ToolDef
	NormalizedInput map[string]any
	ApprovalMode    config.ApprovalMode
	Preview         ApprovalPreview
	WorkDir         string
}

func (e *Executor) Execute(ctx context.Context, toolName string, input map[string]any) (any, error) {
	return e.runPipeline(ctx, executionInput{ToolName: toolName, Input: input})
}

func (e *Executor) runPipeline(ctx context.Context, in executionInput) (any, error)
```

Illustrative internal phases:

- `resolveDefinition(...)`
- `normalizeExecutionInput(...)`
- `authorizeExecution(...)`
- `executeTool(...)`
- `decodeExecutionOutput(...)`
- `shapeExecutionResult(...)`

This is illustrative only. The important part is:

- explicit concrete phases
- a clear internal execution context
- no speculative exported architecture

## Dependency model

The **Tool Execution Pipeline** should continue to own these dependencies through `Executor`:

- tool registry
- approval resolver
- approval responder
- work dir / path policy
- output limit

Prefer using the existing `Executor` fields rather than inventing a new public dependency injection surface.

Possible internal dependency groupings:

- resolution and policy
- approval
- dispatch
- decoding/result shaping

But keep them private unless a real reuse boundary appears.

## Staging strategy

Implement in small stages. Each stage should compile, preserve behavior, and be safe to merge independently.

---

## Stage 1: Establish the Tool Execution Pipeline slot

### Goal

Create the architectural slot for the **Tool Execution Pipeline** without changing behavior.

### Changes

- Add new internal files under `internal/tool`, for example:
  - `execution_pipeline.go`
  - optionally `execution_pipeline_test.go`
- Introduce a concrete internal pipeline entry point.
- Have `Executor.Execute(...)` delegate to that entry point.
- Initially let the pipeline delegate back into the existing logic if needed for behavior parity.

### Deliverable

- Pipeline slot exists and compiles.
- No externally visible behavior change.

### Verification

- `gofmt -w internal/tool/*.go`
- targeted compile or tests for `internal/tool`

### Risks

- Avoid overdesigning internal types before behavior moves.

---

## Stage 2: Move definition lookup and input normalization into explicit phases

### Goal

Make tool resolution and normalized-input creation explicit pipeline phases.

### Changes

- Move into pipeline-owned helpers:
  - registry lookup
  - “tool not registered” failure
  - path-policy validation
- Carry normalized input in an internal execution context.
- Preserve current error semantics.

### Candidate files

- [`internal/tool/executor.go`](/home/luis/Projects/AI/steiner/internal/tool/executor.go:52)
- `internal/tool/execution_pipeline.go`

### Deliverable

- Definition lookup and input normalization become explicit and testable phases.

### Verification

- preserve relevant executor tests
- add phase-level tests for:
  - unknown tool
  - policy-denied input

### Risks

- Be careful not to accidentally duplicate normalization logic between helpers.

---

## Stage 3: Move approval resolution and approval flow into explicit phases

### Goal

Make approval behavior a clear pipeline phase rather than inline control flow.

### Changes

- Move into explicit helpers or pipeline steps:
  - approval mode resolution
  - preview creation
  - auto / denied / prompt branching
  - approval response waiting
  - approval denial shaping
- Keep current semantics for:
  - missing approver
  - approval failure
  - cancellation while awaiting approval

### Candidate files

- [`internal/tool/executor.go`](/home/luis/Projects/AI/steiner/internal/tool/executor.go:71)
- `internal/tool/execution_pipeline.go`

### Deliverable

- Approval handling is owned by one explicit pipeline phase.

### Verification

- preserve existing approval tests
- add targeted tests for:
  - approval denied
  - approval required with no approver
  - context canceled while waiting for approval

### Risks

- Approval behavior is user-visible; preserve error kinds and messages unless intentionally changed.

---

## Stage 4: Move dispatch into explicit handler/subprocess phases

### Goal

Clarify where handler-backed and subprocess-backed tools diverge.

### Changes

- Introduce a dispatch phase that chooses between:
  - handler execution
  - subprocess execution
- Keep both under one pipeline.
- Make work-dir selection explicit inside the dispatch phase:
  - default root
  - bash-specific cwd override if valid
- Keep timeout handling intact.

### Candidate files

- [`internal/tool/executor.go`](/home/luis/Projects/AI/steiner/internal/tool/executor.go:130)
- `internal/tool/execution_pipeline.go`

### Deliverable

- Dispatch path is explicit and locally understandable.

### Verification

- preserve handler-based execution tests
- preserve subprocess execution tests
- add targeted tests for:
  - bash cwd override
  - timeout behavior

### Risks

- Do not accidentally change which work dir is used for bash tools versus non-bash tools.

---

## Stage 5: Move output decoding and structured error shaping into explicit phases

### Goal

Separate execution from output interpretation and final result shaping.

### Changes

- Move JSON envelope decoding into its own phase.
- Move “tool output not valid JSON” and tool-reported failure shaping into explicit helpers.
- Keep execution metadata handling close to decoding/result shaping.
- Preserve current `ToolExecutionError` kinds and metadata population where possible.

### Candidate files

- [`internal/tool/executor.go`](/home/luis/Projects/AI/steiner/internal/tool/executor.go:168)
- `internal/tool/execution_pipeline.go`

### Deliverable

- Output decoding and result/error shaping become explicit module behavior.

### Verification

- preserve invalid JSON tests
- preserve tool-reported failure tests
- add targeted tests for:
  - subprocess failed before valid envelope
  - envelope `OK: false`
  - envelope success path

### Risks

- Be careful not to lose important stderr/stdout metadata in structured errors.

---

## Stage 6: Shrink `Executor.Execute(...)` into orchestration only

### Goal

Make `Executor.Execute(...)` read as orchestration over the pipeline rather than the owner of every phase.

### Changes

- After phases move, simplify `Execute(...)` to:
  - set up the pipeline call
  - return the final result
- Keep remaining helpers only if they still have cohesive responsibilities.
- Re-check whether `runSubprocess(...)` still belongs in `executor.go` or should move alongside pipeline dispatch helpers.

### Candidate files

- [`internal/tool/executor.go`](/home/luis/Projects/AI/steiner/internal/tool/executor.go:52)
- `internal/tool/execution_pipeline.go`

### Deliverable

- `Execute(...)` becomes thin and readable.

### Verification

- `go test ./internal/tool`

### Risks

- If `Execute(...)` remains large, the phase split likely did not go far enough.

---

## Stage 7: Re-center tests on the pipeline seam

### Goal

Test the real execution pipeline interface directly while preserving end-to-end coverage.

### Changes

- Add direct tests in:
  - `internal/tool/execution_pipeline_test.go`
- Cover:
  - definition lookup failure
  - normalization/policy denial
  - approval auto/prompt/denied paths
  - handler success
  - subprocess success
  - invalid JSON
  - tool-reported failure envelope
  - subprocess launch failure
  - cancellation and timeout behavior
- Keep end-to-end executor tests, but rely less exclusively on long-path tests.

### Deliverable

- The real architecture seam has direct tests.

### Verification

- `go test ./internal/tool`
- then broaden to `go test ./...`

### Risks

- Do not remove broad executor tests until equivalent pipeline-level coverage exists.

---

## Stage 8: Cleanup and review

### Goal

Remove transitional duplication and confirm the pipeline is genuinely deep.

### Changes

- Delete transitional helpers that no longer earn their keep.
- Re-check naming and file boundaries.
- Keep the internal execution context compact and understandable.
- Avoid turning the pipeline into a mini-framework.

### Deletion test

Before closing the work, ask:

- If the **Tool Execution Pipeline** were deleted, would tool lookup, normalization, approval flow, dispatch, decoding, and structured error shaping reappear implicitly across `Execute(...)` and scattered helpers?
- If yes, the module is earning its keep.
- If no, the module is still shallow and needs another iteration.

### Final verification

- `gofmt -w` on touched Go files
- `go test ./internal/tool`
- `go test ./...`
- optionally `go build ./...` and `go vet ./...` if broader changes warrant it

### Expected outcome

- Better locality for approval and tool execution behavior
- A clearer internal execution contract
- Less monolithic executor control flow
- Safer future changes to approval, subprocess handling, and structured tool errors
