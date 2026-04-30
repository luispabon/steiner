# Architectural Review Plan

This document records staged architectural refactor plans for `steiner`.

Each subject should be written so another agent can execute it with minimal additional design work. Every subject should preserve package boundaries, keep changes incremental, and prefer test-first or test-adjacent execution where practical.

## Review Status

### Committed subjects

- Subject 1: Deepen the **Interactive Session** module
- Subject 2: Deepen the **Delegation Bootstrap** module

### Candidate queue

These are identified architectural review candidates that have not yet been fully grilled or turned into staged implementation plans. They remain in scope for future subjects unless explicitly rejected or deferred.

1. **Turn Progression** module
2. **Prompt Source Planning** module
3. **Tool Execution Pipeline** module
4. **Provider Request Execution** module

### Deferred or rejected subjects

- None yet

## Subject 1: Deepen the Interactive Session module

### Summary

Refactor interactive mode so the long-lived **Interactive Session** becomes a deep module with a narrow interface. Today, interactive behavior is spread across `cmd/steiner/interactive.go`, TUI callbacks, approval wiring, request snapshot capture, history refresh, and manual compaction flow. The current seam is shallow because the TUI and CLI must know too much about how the interactive run loop works.

The target state is:

- `cmd/steiner` becomes a thin adapter that builds runtime dependencies, constructs the **Interactive Session**, starts the TUI, and closes resources.
- A new `internal/interactive` package owns conversation state, interruption, approvals, model switching, context/config reports, history refresh, and **Manual Compaction**.
- `internal/tui` becomes a presentation adapter that emits typed interactive actions and renders output events.
- Interactive behavior is tested through the **Interactive Session** interface instead of helper methods and callback plumbing.

### Current friction

Current implementation is concentrated in [`cmd/steiner/interactive.go`](/home/luis/Projects/AI/steiner/cmd/steiner/interactive.go:1), especially:

- construction and event wiring at [interactive.go:95](/home/luis/Projects/AI/steiner/cmd/steiner/interactive.go:95)
- select-loop orchestration at [interactive.go:246](/home/luis/Projects/AI/steiner/cmd/steiner/interactive.go:246)
- context/config reports at [interactive.go:280](/home/luis/Projects/AI/steiner/cmd/steiner/interactive.go:280)
- **Manual Compaction** at [interactive.go:302](/home/luis/Projects/AI/steiner/cmd/steiner/interactive.go:302)
- prompt submission and history refresh at [interactive.go:391](/home/luis/Projects/AI/steiner/cmd/steiner/interactive.go:391)

Current TUI callback surface is defined in [`internal/tui/app.go`](/home/luis/Projects/AI/steiner/internal/tui/app.go:27) and copied into [`internal/tui/model.go`](/home/luis/Projects/AI/steiner/internal/tui/model.go:48). This interface is broad and implementation-shaped:

- `OnSubmit`
- `OnContextInspect`
- `OnConfigInspect`
- `OnApproval`
- `OnInterrupt`
- `OnExitRequested`
- `OnSkillToggle`
- `OnModelSwitch`
- `OnClear`
- `OnCompact`

That breadth is the main signal that the seam is shallow.

### Invariants to preserve

- Interactive mode must still use streaming responses by default.
- TUI event rendering must remain event-driven through `internal/output`.
- Approval-gated tools must still block on user approval in interactive mode.
- Model switching must still update the active runtime config and visible provider URL.
- **Manual Compaction** must still share conversation ownership and interruption behavior with normal interactive runs.
- Existing interactive event types should remain stable unless there is a strong reason to change them.
- Package boundaries from `AGENTS.md` must remain intact. In particular:
  - `internal/agent` remains responsible for the agent loop.
  - `internal/prompt` remains responsible for prompt assembly.
  - `internal/tui` remains a presentation layer, not the owner of session logic.

### Proposed target design

Create a new package:

- `internal/interactive`

This package should expose a deep module centered on a session controller.

Illustrative interface shape:

```go
package interactive

import (
	"context"

	"github.com/luispabon/steiner/internal/output"
)

type Action interface {
	isInteractiveAction()
}

type SubmitPrompt struct{ Text string }
type RequestContextReport struct{}
type RequestConfigReport struct{}
type SubmitApproval struct {
	Tool     string
	Mode     string
	Decision string
}
type InterruptActiveRun struct{}
type RequestExit struct{}
type SetSkillEnabled struct {
	Name    string
	Enabled bool
}
type SwitchModel struct{ Name string }
type ClearConversation struct{}
type TriggerManualCompaction struct{}

type Controller interface {
	Handle(ctx context.Context, action Action) error
}

type Session interface {
	Controller
	EventSink() output.EventSink
	Run(ctx context.Context) error
	Close() error
}
```

Notes:

- The exact names may change, but the shape should remain narrow and action-driven.
- `Run(ctx)` may be unnecessary if the session can operate synchronously through `Handle`; confirm during implementation. If it is unnecessary, delete it rather than leaving a speculative seam.
- Avoid exporting extra interfaces unless they are used outside `internal/interactive`.

### Dependency model

Dependencies of the **Interactive Session** should be passed explicitly and grouped by role:

- Run execution dependencies:
  - `cliRunner` or a narrower adapter around it
  - runtime config
  - provider / provider factory
  - history writer
- Output dependencies:
  - base event sink
  - output forwarding for `display_file`
- Approval dependencies:
  - approval coordinator / responder
- UI-facing dependencies:
  - TUI receives only a controller and event sink, not the full runtime

Prefer a concrete dependency struct inside `internal/interactive` instead of spreading constructor parameters across many arguments.

Example:

```go
type Dependencies struct {
	Runtime             cliRuntimeLike
	Runner              runExecutor
	ApprovalCoordinator approvalCoordinatorLike
	RequestSnapshots    snapshotStoreLike
	DisplaySink         *output.ForwardSink
}
```

The actual dependency types should be local, consumer-defined interfaces where needed. Do not introduce a generic `common` or `util` package.

### Staging strategy

Implement in small stages. Each stage should leave the repo green before moving on.

---

## Stage 1: Establish vocabulary and create the package skeleton

### Goal

Create the architectural slot for the **Interactive Session** without changing behavior.

### Changes

- Keep the newly added root [`CONTEXT.md`](/home/luis/Projects/AI/steiner/CONTEXT.md:1) as the vocabulary source for:
  - **Interactive Session**
  - **Context Report**
  - **Manual Compaction**
- Add `internal/interactive/` with skeletal files:
  - `session.go`
  - `actions.go`
  - `deps.go`
- Define initial action types and the session struct, but do not move logic yet.
- Add a constructor like `NewSession(...)`.
- Add placeholder tests to pin down the session surface if useful.

### Deliverable

- New package compiles.
- No behavior change.

### Verification

- `gofmt -w internal/interactive/*.go`
- targeted compile or tests for touched packages

### Risks

- Avoid overdesigning interfaces before moving behavior.
- Do not add unused exported abstractions.

---

## Stage 2: Move passive session state behind the seam

### Goal

Move state ownership into `internal/interactive` while keeping `cmd/steiner/interactive.go` as an adapter.

### Changes

- Move or recreate these concepts inside `internal/interactive`:
  - active run controller
  - conversation slice
  - enabled skills state
  - request snapshot store
  - approval coordinator
- Introduce a concrete `Session` struct owning this state.
- Keep temporary adapter methods in `cmd/steiner/interactive.go` if needed, but make them thin wrappers.
- Avoid moving behavior and state in the same commit unless required for compileability.

### Candidate files

- new: `internal/interactive/state.go`
- new or expanded: `internal/interactive/session.go`
- modified: `cmd/steiner/interactive.go`

### Deliverable

- `interactiveMode` in `cmd/steiner` no longer owns the durable session state directly.

### Verification

- existing interactive tests still pass
- add focused tests for session-owned state transitions if easy to express

### Risks

- Duplicated state during transition. Remove duplicate fields as soon as ownership moves.

---

## Stage 3: Replace callback plumbing with typed interactive actions

### Goal

Narrow the TUI-facing interface so the TUI reports user intent instead of coordinating session mechanics.

### Changes

- Replace most or all callback fields in `internal/tui/app.go` `Config` with a controller dependency.
- The TUI should dispatch typed actions such as:
  - `SubmitPrompt`
  - `RequestContextReport`
  - `RequestConfigReport`
  - `SubmitApproval`
  - `InterruptActiveRun`
  - `RequestExit`
  - `SetSkillEnabled`
  - `SwitchModel`
  - `ClearConversation`
  - `TriggerManualCompaction`
- Update `internal/tui/model.go` to call `Controller.Handle(...)` instead of invoking separate callbacks.
- Keep read-only display config in the TUI config:
  - model name
  - model list
  - model contexts
  - provider base URL
  - theme/accent defaults
  - working/home dir if needed for display only

### Candidate files

- [`internal/tui/app.go`](/home/luis/Projects/AI/steiner/internal/tui/app.go:27)
- [`internal/tui/model.go`](/home/luis/Projects/AI/steiner/internal/tui/model.go:48)
- relevant `internal/tui/*_test.go`
- new `internal/interactive/actions.go`

### Deliverable

- TUI no longer exposes a wide implementation-shaped callback interface.
- The seam becomes centered on an action controller.

### Verification

- update and run `internal/tui` tests
- ensure interactive-specific TUI tests still cover:
  - submit
  - approval submission
  - interrupt
  - exit request
  - context/config report requests
  - model switch
  - clear
  - compaction trigger

### Risks

- TUI tests may be broad and brittle because many of them assume callback fields.
- Preserve behavior first; simplify test fixtures only after parity is restored.

---

## Stage 4: Move session startup and event plumbing into `internal/interactive`

### Goal

Concentrate event sink composition, snapshot capture, and approval responder wiring in the **Interactive Session**.

### Changes

- Move this behavior out of `cmd/steiner/interactive.go`:
  - creating or accepting a `ForwardSink` for `display_file`
  - composing `output.NewMultiSink(...)`
  - intercepting `output.APIRequestEvent` to store request snapshots
  - creating the eventing approver
- The session should expose:
  - `EventSink()` for the TUI and runtime to subscribe to
  - any additional sink wiring needed by runtime internals
- Keep the TUI bridge (`tui.App.EventSink()`) as a presentation adapter concern, but let the session own the runtime-side composition.

### Candidate files

- `internal/interactive/wiring.go`
- `internal/interactive/session.go`
- modified `cmd/steiner/interactive.go`

### Deliverable

- `cmd/steiner` no longer manually assembles the interactive event bus.

### Verification

- add or update tests proving request snapshots still update on API request events
- approval flow still works through eventing approver

### Risks

- Be careful not to invert dependencies so `internal/interactive` depends directly on `internal/tui`.
- The TUI should remain an adapter plugged into the session, not the other way around.

---

## Stage 5: Move prompt submission and history refresh into the session module

### Goal

Make prompt submission a first-class **Interactive Session** behavior.

### Changes

- Move logic from `handleSubmission` into `internal/interactive/run_flow.go`.
- The session should:
  - append the user message
  - start a cancellable run
  - delegate to the runner
  - emit stop/error events consistently
  - refresh history after successful submission
  - replace session conversation with the resulting conversation
- If helpful, introduce a private helper for “run with interrupt ownership”.

### Candidate files

- `internal/interactive/run_flow.go`
- modified `cmd/steiner/interactive.go`
- maybe narrowed `cliRunner` adapter interface in `cmd/steiner` or `internal/interactive`

### Deliverable

- Prompt submission behavior is owned entirely by the **Interactive Session**.

### Verification

- move or rewrite tests so they target session behavior rather than `interactiveMode.handleSubmission`
- preserve history-loaded event behavior after recording prompts

### Risks

- The current `cliRunner` may be too concrete. If so, define a small consumer-owned interface in `internal/interactive`.
- Do not drag broad CLI concerns into the new package.

---

## Stage 6: Move Context Report and resolved-config report generation

### Goal

Treat reports as session-owned queries, not ad hoc command handlers in `cmd/steiner`.

### Changes

- Move `emitContextReport` logic into `internal/interactive/reports.go`.
- Move resolved config report generation and emission into the same package.
- The session should respond to:
  - `RequestContextReport`
  - `RequestConfigReport`
- Keep markdown/report formatting close to the session if the report is session-owned.
- If `buildConfigOverlayReport` is useful elsewhere, move it with a narrow name rather than leaving it under `cmd/steiner`.

### Candidate files

- `internal/interactive/reports.go`
- modified `cmd/steiner/interactive.go`

### Deliverable

- Report generation lives behind the **Interactive Session** seam.

### Verification

- preserve existing context/config overlay behavior in TUI tests
- add session tests for “no request recorded yet” and failure paths

### Risks

- Avoid mixing rendering concerns into the TUI. The session should emit report events; the TUI should render them.

---

## Stage 7: Move Manual Compaction into the session module

### Goal

Keep **Manual Compaction** inside the same deep module as normal interactive execution.

### Changes

- Move:
  - model selection
  - provider override selection
  - compaction request assembly
  - compaction run lifecycle
  - compaction-specific event emission
- Preserve shared interruption semantics with normal runs.
- Decide whether `runManualCompaction` remains a dedicated helper inside `internal/interactive` or becomes a more general “run operation with interrupt ownership” helper.
- Keep compaction internal to the session unless a second real caller appears.

### Candidate files

- `internal/interactive/compaction.go`
- `internal/interactive/run_flow.go`

### Deliverable

- `cmd/steiner` no longer owns **Manual Compaction** logic.

### Verification

- port existing compaction tests from [`cmd/steiner/interactive_test.go`](/home/luis/Projects/AI/steiner/cmd/steiner/interactive_test.go:33) to the session package where appropriate
- verify:
  - lifecycle events
  - cancellation
  - controller cleanup
  - conversation replacement

### Risks

- Be careful with provider factory access and config mutation during model selection.
- Preserve exact event ordering where existing tests depend on it, unless explicitly simplifying those guarantees.

---

## Stage 8: Collapse `cmd/steiner/interactive.go` into a thin adapter

### Goal

Leave the CLI file as composition code only.

### Changes

- `runInteractiveMode` should do only:
  - build runtime
  - build session dependencies
  - create session
  - create TUI app
  - connect TUI event sink to session if needed
  - run and close
- Delete now-redundant helpers and structs from `cmd/steiner/interactive.go`:
  - `interactiveMode`
  - `activeRunController`
  - local report helpers
  - submission/compaction methods
- Keep only thin startup helpers if they still add clarity.

### Deliverable

- `cmd/steiner/interactive.go` becomes short and compositional.

### Verification

- targeted tests for `cmd/steiner`
- full package tests for touched areas

### Risks

- If the adapter remains large, stop and re-check whether some runtime-specific concerns still belong in session dependencies.

---

## Stage 9: Re-center the tests on the session seam

### Goal

Make the **Interactive Session** interface the primary test surface.

### Changes

- Add `internal/interactive/session_test.go`.
- Add focused tests for:
  - prompt submission success
  - prompt submission error
  - interruption
  - approval submission
  - context report request with snapshot present
  - context report request without snapshot
  - config report emission
  - clear conversation
  - model switch success/failure
  - **Manual Compaction** success/error/cancel
- Reduce or delete tests in `cmd/steiner/interactive_test.go` that only covered internal mechanics now owned by the session.
- Keep TUI tests focused on rendering and action dispatch, not session orchestration.

### Deliverable

- Tests describe the real interface of the module.

### Verification

- run targeted tests:
  - `go test ./cmd/steiner -run TestInteractive`
  - `go test ./internal/tui`
  - `go test ./internal/interactive`
- then broaden as practical:
  - `go test ./...`

### Risks

- Do not delete coverage until equivalent session-level tests exist.
- Some event-order assertions may still be worth keeping if they define important user-visible behavior.

---

## Stage 10: Cleanup and review

### Goal

Remove transitional duplication and confirm the module is actually deep.

### Changes

- Delete transitional wrappers and compatibility shims.
- Re-check the `internal/tui` config surface and remove any remaining action-specific leakage.
- Re-check `internal/interactive` for speculative interfaces or split files that do not earn their keep.
- Keep file sizes reasonable; split files by domain responsibility, not by arbitrary helper count.

### Deletion test

Before closing the work, ask:

- If `internal/interactive` were deleted, would complexity reappear across CLI, TUI, approval wiring, compaction, and reporting?
- If yes, the module is earning its keep.
- If no, the module is still shallow and needs another iteration.

### Final verification

- `gofmt -w` on touched Go files
- targeted tests for changed packages
- `go test ./...`
- optionally `go build ./...` and `go vet ./...` if the change set is large enough to justify it

### Expected outcome

- Better locality for interactive behavior
- More leverage from a smaller TUI-facing interface
- Cleaner ownership of **Context Report** and **Manual Compaction**
- A more AI-navigable seam for future interactive-mode changes

---

## Template for future architectural review subjects

Use this structure for the next subjects appended to this document:

1. Summary
2. Current friction
3. Invariants to preserve
4. Proposed target design
5. Dependency model
6. Staging strategy
7. Stage 1..N with:
   - Goal
   - Changes
   - Candidate files
   - Deliverable
   - Verification
   - Risks
8. Cleanup and review
