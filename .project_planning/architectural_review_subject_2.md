# Architectural Review Subject 2

## Subject

Deepen the **Delegation Bootstrap** module.

### Summary

Refactor child delegation setup so the **Delegation Bootstrap** becomes a deep module with one cohesive interface. Today, the setup of a delegated child run is split across handler parsing, limit derivation, child prompt scaffolding, child tool filtering, approval rewriting, and run request construction. The current seams are shallow because the caller must stitch together too many low-level steps to create a child agent run.

The target state is:

- `internal/delegation` exposes a single bootstrap flow that turns a `DelegationSpec` plus dependencies into an executable child run.
- Child prompt construction, child tool visibility, child execution policy, limit tightening, and working-directory semantics are owned in one place.
- The delegate tool handler becomes a thin adapter that validates raw tool input and invokes the **Delegation Bootstrap**.
- Tests move from scattered helper coverage toward the real child-run assembly surface.

### Current friction

Current behavior is spread across:

- handler input parsing and child run kickoff in [`internal/delegation/tool.go`](/home/luis/Projects/AI/steiner/internal/delegation/tool.go:48)
- prompt / registry / executor assembly in [`internal/delegation/scaffold.go`](/home/luis/Projects/AI/steiner/internal/delegation/scaffold.go:25)
- limit defaults and tighten-only overrides in [`internal/delegation/limits.go`](/home/luis/Projects/AI/steiner/internal/delegation/limits.go:7)

Specific friction points:

- `NewDelegateHandler` parses input, derives limits, generates agent IDs, filters tools, builds an `agent.RunRequest`, and executes delegation in one flow ([tool.go:49](/home/luis/Projects/AI/steiner/internal/delegation/tool.go:49)).
- `buildChildRunRequest` calls `scaffoldChildContext`, discards its error, rebuilds registries again, injects the system prompt as a conversation message, and calls `os.Getwd()` internally ([scaffold.go:71](/home/luis/Projects/AI/steiner/internal/delegation/scaffold.go:71)).
- Tool filtering and execution-policy rewriting are split across `BuildChildToolRegistry` and `buildChildExecutionRegistry`, even though callers always need both ([scaffold.go:39](/home/luis/Projects/AI/steiner/internal/delegation/scaffold.go:39), [scaffold.go:58](/home/luis/Projects/AI/steiner/internal/delegation/scaffold.go:58)).
- The current tests mostly prove fragments:
  - schema and raw handler parsing in [`tool_test.go`](/home/luis/Projects/AI/steiner/internal/delegation/tool_test.go:49)
  - prompt defaults and tool exclusion in [`scaffold_test.go`](/home/luis/Projects/AI/steiner/internal/delegation/scaffold_test.go:10)
  - limit merging in [`limits.go`](/home/luis/Projects/AI/steiner/internal/delegation/limits.go:28)

That fragmentation suggests the real module interface is missing.

### Invariants to preserve

- A delegated child agent must still be unable to delegate further by default.
- Sub-agent limits must still use tighten-only semantics relative to configured defaults.
- Child execution must still auto-approve allowed tools inside the child run, unless the design is intentionally revisited later.
- The delegate tool schema and externally visible delegate tool name should remain stable unless there is a compelling reason to change them.
- Child prompt setup must still support:
  - required task
  - optional additional context
  - optional system prompt override
  - optional model override
- Existing `DelegationResult` behavior and overall delegation execution flow should remain intact.
- Package boundaries from `AGENTS.md` must remain intact:
  - `internal/agent` owns the run loop
  - `internal/prompt` owns prompt assembly
  - `internal/delegation` owns delegation contracts and scaffolding

### Proposed target design

Keep the work in `internal/delegation`, but center it on one module named around the repo term **Delegation Bootstrap**.

Chosen design direction for this subject:

- Use one concrete bootstrap entry point first, not a formal bootstrap interface.
- Preserve current child prompt semantics initially, but move those semantics behind the bootstrap seam.
- Make child work-directory semantics explicit now; do not retain ambient `os.Getwd()` lookup inside child-run assembly.

Illustrative interface shape:

```go
package delegation

import (
	"context"

	"github.com/luispabon/steiner/internal/agent"
)

type BootstrapInput struct {
	Spec      DelegationSpec
	WorkDir   string
	ParentReg ToolRegistryView
}

type BootstrapResult struct {
	RunRequest agent.RunRequest
	VisibleReg ProviderToolRegistryView
}

type Bootstrapper interface {
	Build(ctx context.Context, input BootstrapInput) (BootstrapResult, error)
}
```

Or, if the implementation is simpler and no second adapter exists:

```go
func BuildChildRun(ctx context.Context, deps BootstrapDeps, spec DelegationSpec) (agent.RunRequest, error)
```

Recommendation:

- Prefer the function form first unless a real second adapter appears.
- Do not introduce speculative public interfaces just to make the design look abstract.
- The deep module is the ownership of the setup flow, not the presence of a large interface hierarchy.

Decision:

- This subject will implement the function form first.
- Do not add a `Bootstrapper` interface during this refactor unless a second real adapter appears while doing the work.

### Dependency model

The **Delegation Bootstrap** should own these dependencies as one bundle:

- `provider.Provider`
- parent `tool.Registry`
- `config.SubAgentConfig`
- `output.EventSink`
- working directory or execution root

Prefer one dependency struct, for example:

```go
type BootstrapDeps struct {
	Provider    provider.Provider
	ParentReg   *tool.Registry
	SubAgentCfg config.SubAgentConfig
	Events      output.EventSink
	WorkDir     string
}
```

This lets the bootstrap own:

- limit defaults
- limit overrides
- child prompt scaffolding
- child tool visibility
- child execution registry policy
- child executor creation
- `agent.RunRequest` assembly

The delegate tool handler should then own only:

- raw JSON-ish input extraction
- validation of required fields
- conversion into `DelegationSpec`
- call to bootstrap
- call to `SpawnDelegate`

Decision consequences:

- The handler remains the raw-input adapter.
- The bootstrap owns typed child-run assembly.
- Tests should primarily assert the assembled child run request, not the existence of interchangeable bootstrap implementations.

### Design decisions locked for execution

These decisions are settled for this subject and should not be re-litigated during implementation unless a concrete blocker appears.

#### Decision 1: use a concrete bootstrap function

Chosen direction:

- Start with a concrete entry point such as `BuildChildRun(ctx, deps, spec) (agent.RunRequest, error)`.

Reason:

- There is only one real adapter today.
- An interface here would be hypothetical and likely shallow.
- The current friction is scattered ownership, not lack of substitutability.

Implication for implementation:

- Keep the public surface small.
- Prefer private helpers under the bootstrap function over exported interface types.

#### Decision 2: preserve child prompt behavior first

Chosen direction:

- Keep the current child prompt behavior initially, including current system-prompt semantics, while moving the logic behind the bootstrap seam.

Reason:

- This keeps the first refactor architectural rather than behavioral.
- It minimizes risk to delegated model behavior while improving locality.

Implication for implementation:

- Stage 3 should move prompt construction ownership first.
- Any redesign of how the child system prompt flows through `prompt.AssemblyOptions` is out of scope for this subject unless needed for correctness.

#### Decision 3: make work dir explicit now

Chosen direction:

- Pass the child work dir explicitly through bootstrap dependencies.
- Remove `os.Getwd()` from child-run assembly as part of this subject.

Reason:

- Ambient process state is a real source of hidden coupling.
- Explicit work-dir semantics are easier to test and reason about.
- This is a genuine deepening move, not speculative flexibility.

Implication for implementation:

- Runtime wiring that constructs the delegate handler may need to pass work dir through the bootstrap dependency struct.
- Tests should assert the child executor uses the provided work dir.

### Staging strategy

Implement in small stages. Each stage should compile and leave delegation behavior shippable.

---

## Stage 1: Establish vocabulary and define the bootstrap seam

### Goal

Create the explicit architectural slot for the **Delegation Bootstrap** without changing behavior.

### Changes

- Keep the `CONTEXT.md` term **Delegation Bootstrap** as the vocabulary anchor.
- Add new file(s) under `internal/delegation` such as:
  - `bootstrap.go`
  - optionally `bootstrap_types.go`
- Introduce a dependency struct for bootstrap assembly.
- Introduce a first bootstrap entry point:
  - either `BuildChildRun(...)`
  - or `Bootstrap(...)/Build(...)`
- Initially let the bootstrap delegate internally to existing helpers if that keeps behavior unchanged.

### Deliverable

- New bootstrap seam exists.
- No behavior change yet.

### Verification

- `gofmt -w internal/delegation/*.go`
- targeted delegation tests compile and pass

### Risks

- Avoid inventing both an interface and a function if one is enough.
- Avoid moving behavior and renaming everything at once.

---

## Stage 2: Centralize limit derivation into the bootstrap flow

### Goal

Make limit setup part of one delegation assembly path instead of handler-specific glue.

### Changes

- Move the `DefaultLimits` + override application flow behind the bootstrap seam.
- Introduce a helper local to bootstrap if useful, for example:
  - `deriveLimits(cfg config.SubAgentConfig, raw overrides) DelegationLimits`
- Decide whether parsing raw tool input remains in the handler; recommended split:
  - handler parses raw values
  - bootstrap consumes typed `DelegationSpec`
- Keep `ApplyOverrides` if it still earns its keep.
- Consider renaming `OutputLimitTokens` later only if needed; do not mix naming cleanup with behavior movement in this stage.

### Candidate files

- `internal/delegation/limits.go`
- `internal/delegation/bootstrap.go`
- `internal/delegation/tool.go`

### Deliverable

- The handler no longer owns limit semantics directly.

### Verification

- preserve existing tests for tighten-only semantics
- add one bootstrap-level test proving configured defaults plus overrides produce the child run limits

### Risks

- Do not lose the current tighten-only behavior when moving code.

---

## Stage 3: Move child prompt scaffolding into the bootstrap module

### Goal

Make child prompt construction a first-class delegation behavior.

### Changes

- Fold `scaffoldChildContext` and the `task + Additional context` conversation-building logic into the bootstrap flow.
- Stop injecting the system prompt via ad hoc conversation prepending unless that remains the best fit after review.
- Re-examine whether the child system prompt should instead live in `prompt.AssemblyOptions` preamble configuration; if changing this, do it only if behavior remains equivalent and tests clearly prove it.
- Stop discarding the error from `scaffoldChildContext`. If that helper remains, its error must be handled.

### Candidate files

- `internal/delegation/scaffold.go`
- `internal/delegation/bootstrap.go`
- `internal/delegation/scaffold_test.go`

### Deliverable

- Child prompt setup is owned entirely by the **Delegation Bootstrap**.

### Verification

- preserve system prompt default/override behavior
- preserve task + additional context formatting
- add bootstrap-level tests that assert the resulting `agent.RunRequest.Prompt`

### Risks

- Be careful not to unintentionally alter prompt order in a way that changes model behavior.

---

## Stage 4: Consolidate child tool visibility and execution policy

### Goal

Turn child tool preparation into one cohesive module behavior instead of two thin helpers.

### Changes

- Revisit `BuildChildToolRegistry` and `buildChildExecutionRegistry`.
- Replace the two-step flow with one cohesive helper inside the bootstrap path, for example:
  - `buildChildRegistries(...) (visible *tool.Registry, executable *tool.Registry)`
- Own both concerns together:
  - removing the delegate tool from the child-visible registry
  - setting child tool approval mode for execution
- Keep any standalone helper public only if another package actually needs it.
- If no external package needs `BuildChildToolRegistry`, consider making it private after callers move.

### Candidate files

- `internal/delegation/scaffold.go`
- `internal/delegation/bootstrap.go`
- `internal/delegation/scaffold_test.go`

### Deliverable

- Callers no longer manually compose child visibility and child execution policy.

### Verification

- preserve exclusion of the delegate tool
- preserve auto-approval behavior for child execution registry
- add one bootstrap-level test that checks both visible provider specs and executable registry policy together

### Risks

- Do not accidentally change the tools exposed to the model versus the tools executable by the child agent.

---

## Stage 5: Make working-directory semantics explicit

### Goal

Remove ambient `os.Getwd()` dependence from child-run assembly.

### Changes

- Stop reading the working directory inside `buildChildRunRequest` ([scaffold.go:97](/home/luis/Projects/AI/steiner/internal/delegation/scaffold.go:97)).
- Pass the execution root or work dir explicitly through bootstrap dependencies.
- Ensure the child executor gets a known root derived by the caller.
- Decide whether delegation should use:
  - the parent runtime work dir
  - a normalized execution root
- Document the choice in code comments or test names.

### Candidate files

- `internal/delegation/bootstrap.go`
- `internal/delegation/tool.go`
- any caller wiring the delegate tool

### Deliverable

- Child execution root becomes explicit and testable.

### Verification

- add tests proving the child executor is built with the provided work dir
- no hidden dependency on process cwd remains

### Risks

- This stage may require touching the runtime wiring where `NewDelegateHandler` is constructed.

---

## Stage 6: Make the delegate tool handler a thin adapter

### Goal

Reduce `NewDelegateHandler` to input validation and orchestration only.

### Changes

- Keep only these responsibilities in the handler:
  - read and validate raw input fields
  - parse `timeout`
  - build a `DelegationSpec`
  - call the **Delegation Bootstrap**
  - call `SpawnDelegate`
- Move agent ID generation either:
  - into the bootstrap flow, if agent identity is part of child run assembly
  - or into a small dedicated helper invoked by the handler
- Ensure the handler no longer rebuilds registries or run requests itself.

### Candidate files

- `internal/delegation/tool.go`
- `internal/delegation/bootstrap.go`

### Deliverable

- `NewDelegateHandler` becomes short and adapter-like.

### Verification

- keep schema tests
- keep empty-task validation tests
- add bootstrap invocation tests if useful through spies/stubs

### Risks

- Do not over-mock. Prefer checking the resulting `DelegationSpec` and `agent.RunRequest` surface where possible.

---

## Stage 7: Re-center tests on the bootstrap seam

### Goal

Test the real child-run assembly surface instead of mostly isolated helper fragments.

### Changes

- Add `internal/delegation/bootstrap_test.go`.
- Cover:
  - default system prompt
  - provided system prompt
  - task + context formatting
  - default limits
  - tightened overrides
  - child visible tools exclude `delegate`
  - child executable tools get intended approval mode
  - explicit work dir is used
  - resulting `agent.RunRequest` has expected prompt/tools/executor/limits fields
- Keep some focused unit tests for limit merging if they remain useful.
- Delete or shrink helper-specific tests only after equivalent bootstrap-level coverage exists.

### Deliverable

- The **Delegation Bootstrap** is the primary test surface.

### Verification

- `go test ./internal/delegation`
- broaden to `go test ./...` after touched packages are stable

### Risks

- Some tests may need light refactoring if `tool.Registry` internals are not directly inspectable. Prefer behavior assertions over implementation peeking.

---

## Stage 8: Cleanup and API review

### Goal

Remove transitional duplication and verify the module is actually deep.

### Changes

- Delete or privatize transitional helpers that no longer earn their keep:
  - `buildChildRunRequest`
  - maybe `BuildChildToolRegistry` if no external callers remain
  - maybe `scaffoldChildContext` if its responsibilities are fully absorbed
- Keep `limits.go` only if it still has a clear, cohesive role.
- Re-check naming:
  - prefer names that reflect the repo term **Delegation Bootstrap**
  - avoid leaving “scaffold” as the main public concept if the module now owns more than scaffolding

### Deletion test

Before closing the work, ask:

- If the bootstrap module were deleted, would child prompt setup, child tool visibility, limit semantics, work-dir semantics, and run request assembly reappear across the handler and callers?
- If yes, the module is earning its keep.
- If no, the module is still shallow and needs another iteration.

### Final verification

- `gofmt -w` on touched Go files
- `go test ./internal/delegation`
- `go test ./...`
- optionally `go build ./...` if runtime wiring changed materially

### Expected outcome

- Better locality for child delegation setup
- More leverage from a single bootstrap seam
- Less handler glue and less hidden ambient behavior
- A clearer execution surface for future delegation features
