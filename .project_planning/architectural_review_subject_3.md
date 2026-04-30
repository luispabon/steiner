# Architectural Review Subject 3

## Subject

Deepen the **Turn Progression** module in the agent loop.

## Summary

Refactor the agent loop so one deep module owns the full single-turn transition. Today, `internal/agent/runner.go` mixes outer-loop stop policy with turn-level behavior: prompt assembly, budget fit checks, compaction, model execution, tool execution, transcript mutation, token accounting, and turn-level event ordering.

The target state is:

- `Runner` owns only run-level concerns:
  - initial state setup
  - outer loop repetition
  - max-turn / max-token / cancellation stop policy
  - final stop behavior
- A new **Turn Progression** module owns one turn end to end:
  - assemble prompt
  - fit request to model budget
  - trigger compaction when needed
  - execute the model call
  - apply assistant response
  - execute tool calls
  - update conversation state
  - emit turn-level events
  - update turn token usage

This subject should be implemented incrementally, not as a big-bang rewrite.

## Current friction

The current logic is concentrated in [`internal/agent/runner.go`](/home/luis/Projects/AI/steiner/internal/agent/runner.go:1), especially:

- outer loop and stop checks at [runner.go:46](/home/luis/Projects/AI/steiner/internal/agent/runner.go:46)
- turn assembly, fit check, and compaction path at [runner.go:101](/home/luis/Projects/AI/steiner/internal/agent/runner.go:101)
- assistant response application at [runner.go:154](/home/luis/Projects/AI/steiner/internal/agent/runner.go:154)
- tool-call execution and transcript updates at [runner.go:188](/home/luis/Projects/AI/steiner/internal/agent/runner.go:188)

The main architecture problem is that the seam is too broad:

- `Runner` knows both run-level and turn-level concerns.
- Turn-level event ordering is entangled with state mutation.
- Compaction is coordinated from the same module as outer-loop termination.
- Tool-call execution is part of the same module as budget fit logic.
- Tests naturally become choreography-heavy because the real interface is “advance a turn,” but the code is structured around one large runner implementation.

Current tests in [`internal/agent/runner_test.go`](/home/luis/Projects/AI/steiner/internal/agent/runner_test.go:18) already reflect this. They often assert long event sequences because there is no narrower test surface for a single-turn transition.

## Invariants to preserve

- The run loop must still stop on:
  - context cancellation
  - max turns
  - max tokens
  - completion with no tool calls
  - unrecoverable errors
- Prompt assembly must remain owned by `internal/prompt`.
- Compaction must continue to be triggered by request-fit failure against the model budget.
- Tool-call execution must still update the conversation and event stream in the same observable way unless explicitly simplified in a later subject.
- Turn token accounting must remain correct for model responses.
- Existing `RunState` semantics must remain intact unless a targeted change is justified and tested.
- Package boundaries from `AGENTS.md` must remain intact:
  - `internal/agent` owns loop orchestration and state
  - `internal/prompt` owns prompt assembly
  - `internal/tool` owns tool execution
  - `internal/provider` owns model transport

## Design decisions locked for execution

These decisions are settled for this subject and should not be re-litigated during implementation unless a concrete blocker appears.

### Decision 1: create a concrete Turn Progression module

Chosen direction:

- Use a concrete module inside `internal/agent`, not a speculative interface hierarchy.

Reason:

- The problem is ownership and locality, not substitutability.
- There is only one real turn progression path today.
- Helper extraction alone would be too shallow, but formal interfaces would also be overdesigned.

Implication for implementation:

- Prefer a concrete type or concrete function set under `internal/agent`.
- Keep exported surface area minimal or zero if cross-package export is not needed.

### Decision 2: keep compaction inside Turn Progression

Chosen direction:

- **Turn Progression** owns the fit-check and compaction path for a turn.

Reason:

- Compaction is part of advancing one turn under budget constraints.
- Splitting it now would create a likely hypothetical seam.

Implication for implementation:

- Do not create a separate top-level compaction module for this subject.
- If compaction helpers move, they should still remain behind the turn seam.

### Decision 3: keep tool-call execution inside Turn Progression

Chosen direction:

- **Turn Progression** owns tool-call execution and tool transcript application for the turn.

Reason:

- Tool calls are part of the same turn transition that processes the model response.
- Splitting them now would likely produce a shallow seam where Turn Progression still coordinates everything important.

Implication for implementation:

- Do not split tool-call execution into a separate subject within this refactor.
- Internal helpers are fine; a separate architectural subject is not.

### Decision 4: Runner becomes outer-loop owner only

Chosen direction:

- `Runner` should keep:
  - initial state creation
  - run loop repetition
  - stop-condition checks
  - final stop behavior
- `Runner` should stop owning turn-level orchestration details.

Reason:

- This gives a clean seam between run policy and single-turn progression.

Implication for implementation:

- If a method still both enforces outer-loop stop policy and performs turn work, it probably belongs partly in the new turn module.

## Proposed target design

Introduce a turn-focused concrete module inside `internal/agent`.

Illustrative shape:

```go
package agent

import "context"

type turnProgressor struct{}

type turnInput struct {
	Request            RunRequest
	State              RunState
	BasePrompt         prompt.AssemblyOptions
	CompactionHistory  map[string]bool
	CompactionCount    *int
}

type turnOutcome struct {
	State    RunState
	Stop     bool
	Retry    bool
	Error    error
}

func (p turnProgressor) advance(ctx context.Context, in turnInput) turnOutcome
```

This is illustrative only. The exact names may change, but the design intent should hold:

- concrete internal types
- explicit turn input
- explicit turn outcome
- one owning place for the turn transition

Recommended outcome shape:

- return updated `RunState`
- distinguish:
  - continue outer loop
  - stop run
  - retry after compaction or another turn-local state change
  - error

Avoid using boolean soup if it starts to get unclear. If needed, replace `Stop` / `Retry` with a small internal enum-like status.

## Dependency model

The **Turn Progression** module should consume the same dependencies already present in `RunRequest` rather than inventing new cross-package abstractions:

- `provider.Provider`
- `ToolExecutor`
- tool specs
- prompt assembly options
- model budget
- model name / request params
- event sink
- limits as needed

It should also own the turn-local relationship among:

- prompt assembly result
- provider chat request
- provider chat response
- token accounting
- tool result application

Prefer passing a concrete `RunRequest` plus explicit turn state rather than exploding constructor arguments.

## Staging strategy

Implement in small stages. Each stage should compile, preserve behavior, and be safe to merge independently.

---

## Stage 1: Establish the Turn Progression slot

### Goal

Create the architectural slot for **Turn Progression** without changing behavior.

### Changes

- Add new internal files under `internal/agent`, for example:
  - `turn_progression.go`
  - optionally `turn_progression_types.go`
- Define the concrete turn input/output types.
- Add a concrete progressor type or function entry point.
- Initially let the new entry point delegate back to existing runner methods if needed for behavior parity.

### Deliverable

- New turn module exists and compiles.
- No externally visible behavior change.

### Verification

- `gofmt -w internal/agent/*.go`
- targeted compile or tests for `internal/agent`

### Risks

- Avoid creating too many new files before behavior actually moves.
- Avoid exporting new types unless cross-package use requires it.

---

## Stage 2: Move prompt assembly and fit-check logic behind the turn seam

### Goal

Make prompt assembly, chat request creation, and budget fit checks part of **Turn Progression**.

### Changes

- Move from `Runner.runTurn` into the new turn module:
  - prompt assembly
  - assembly diagnostics emission
  - chat request construction
  - model-budget fit check
  - request token diagnostics emission
- Keep behavior unchanged.
- Decide how the outcome expresses “request does not fit and compaction should run”.

### Candidate files

- [`internal/agent/runner.go`](/home/luis/Projects/AI/steiner/internal/agent/runner.go:101)
- `internal/agent/turn_progression.go`

### Deliverable

- `Runner` no longer owns prompt assembly and fit-check mechanics directly.

### Verification

- preserve relevant `runner_test.go` behavior
- add turn-level tests if possible for:
  - successful fit
  - fit failure path

### Risks

- Avoid hidden mutation of `RunState` during assembly unless clearly contained in the new turn input/output path.

---

## Stage 3: Move compaction flow behind the turn seam

### Goal

Keep compaction as part of **Turn Progression** rather than a separate architecture track.

### Changes

- Move the `!fit.Fits` path and `compactConversationForBudget(...)` coordination into the turn module.
- Express the post-compaction outcome clearly:
  - either “retry outer loop now”
  - or “state changed, continue”
- Preserve compaction-related event emission and error handling.

### Candidate files

- `internal/agent/turn_progression.go`
- existing compaction helpers in `internal/agent`

### Deliverable

- The turn module owns what happens when a request does not fit.

### Verification

- existing compaction tests still pass
- add turn-level tests for:
  - compaction triggered
  - compaction succeeded and run should continue
  - compaction failure path

### Risks

- Be explicit about whether compaction causes a retry of the same conceptual turn or a new loop iteration with updated state. The current behavior must be preserved.

---

## Stage 4: Move model-call execution and assistant response handling

### Goal

Put the model-call phase and assistant-message application fully inside **Turn Progression**.

### Changes

- Move:
  - turn-start/model-call-start events
  - `completeModelCall(...)`
  - model-call finished events
  - assistant message event emission
  - token accounting
  - assistant transcript/state mutation
- Keep cancellation handling behavior intact.

### Candidate files

- [`internal/agent/runner.go`](/home/luis/Projects/AI/steiner/internal/agent/runner.go:134)
- `internal/agent/turn_progression.go`

### Deliverable

- The turn module owns model execution and assistant response application.

### Verification

- preserve streaming and non-streaming response behavior
- preserve token accounting semantics
- preserve cancellation behavior in existing tests

### Risks

- Event ordering is important here. Move logic carefully and preserve observable behavior.

---

## Stage 5: Move tool-call execution and tool transcript application

### Goal

Complete the turn seam by moving tool-call behavior into **Turn Progression**.

### Changes

- Move from `executeToolCalls(...)`:
  - tool-call started events
  - executor invocation
  - cancellation handling
  - tool error formatting
  - tool preview construction
  - tool finished events
  - tool message append to conversation/lineage
- Keep the final turn-finished behavior associated with the turn module.

### Candidate files

- [`internal/agent/runner.go`](/home/luis/Projects/AI/steiner/internal/agent/runner.go:188)
- `internal/agent/turn_progression.go`

### Deliverable

- Tool-call execution is fully owned by **Turn Progression**.

### Verification

- preserve `TestRunnerExecutesToolThenFinalAnswer`
- preserve `TestRunnerPreservesToolResultContentWhileEmittingInternalPreview`
- add turn-level tests if useful for:
  - tool success
  - tool failure
  - cancellation during tool execution

### Risks

- Avoid duplicating tool-message append logic in multiple places.

---

## Stage 6: Shrink Runner to outer-loop ownership

### Goal

Make `Runner` clearly responsible for run-level policy only.

### Changes

- Simplify `Runner.Run(...)` so it:
  - initializes state
  - checks cancellation / max-turn / max-token stop policy
  - invokes **Turn Progression**
  - interprets turn outcome
  - emits final stop behavior
- Remove or simplify `runTurn`, `handleModelResponse`, and `executeToolCalls` once their responsibilities have moved.
- Keep helper functions only if they remain cohesive and clearly owned.

### Candidate files

- [`internal/agent/runner.go`](/home/luis/Projects/AI/steiner/internal/agent/runner.go:46)
- `internal/agent/turn_progression.go`

### Deliverable

- `Runner` reads as an outer loop rather than a full run engine and turn engine combined.

### Verification

- existing `internal/agent` tests still pass
- add focused tests for:
  - max-turn stop
  - max-token stop
  - immediate cancellation

### Risks

- If `Runner` still looks large after this stage, re-check whether run-level and turn-level concerns were actually separated.

---

## Stage 7: Re-center tests on the turn seam

### Goal

Make **Turn Progression** the primary test surface for single-turn behavior.

### Changes

- Add new tests, for example in:
  - `internal/agent/turn_progression_test.go`
- Cover:
  - assembled request success path
  - fit failure and compaction path
  - model-call success
  - model-call cancellation
  - tool-call success
  - tool-call failure
  - tool-call cancellation
  - assistant response with no tool calls leading to stop
- Keep some `Runner` tests, but shift them toward run-level policy instead of detailed turn choreography.

### Deliverable

- The real interface of the module has direct tests.

### Verification

- `go test ./internal/agent`
- then broaden to `go test ./...`

### Risks

- Do not delete broad runner tests until equivalent turn-level coverage exists.
- Keep only the event-order assertions that are truly user-visible or contract-level.

---

## Stage 8: Cleanup and review

### Goal

Remove transitional duplication and confirm the refactor created a genuinely deep module.

### Changes

- Delete transitional wrappers or compatibility helpers that no longer earn their keep.
- Re-check naming and file boundaries.
- Keep files split by domain responsibility, not by arbitrary helper count.
- Re-check whether the turn outcome type is simple enough to understand quickly.

### Deletion test

Before closing the work, ask:

- If **Turn Progression** were deleted, would prompt assembly, fit checks, compaction, model execution, tool handling, and turn-level event ordering reappear across `Runner` and multiple helpers?
- If yes, the module is earning its keep.
- If no, the module is still shallow and needs another iteration.

### Final verification

- `gofmt -w` on touched Go files
- `go test ./internal/agent`
- `go test ./...`
- optionally `go build ./...` and `go vet ./...` if the change set is large enough to justify them

### Expected outcome

- Better locality for single-turn agent behavior
- Cleaner separation between run-level policy and turn-level progression
- Higher leverage test surface for future loop changes
- Less choreography pressure on `Runner` tests
