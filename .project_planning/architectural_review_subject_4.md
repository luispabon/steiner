# Architectural Review Subject 4

## Subject

Deepen the **Prompt Source Planning** module in `internal/prompt`.

## Summary

Refactor prompt assembly so one deep module owns prompt-source ordering and budget decisions before rendering provider messages. Today, `internal/prompt` mostly works, but the real interface is implicit: which sources are included, in what order, under which budgets, and which become blocks versus raw conversation pass-through.

The target state is:

- A concrete **Prompt Source Planning** module decides:
  - which prompt sources are included
  - in what order
  - how source budgets apply
  - which sources become `ContextBlock`s
  - where raw conversation and tool summaries are placed
- Rendering then converts that plan into:
  - `ContextBlock`s
  - provider messages
- Existing prompt behavior remains stable at first; the architecture changes before prompt semantics do.

This subject should be implemented incrementally and merged stage by stage.

## Current friction

Current logic is concentrated in:

- [`internal/prompt/assemble.go`](/home/luis/Projects/AI/steiner/internal/prompt/assemble.go:1)
- [`internal/prompt/assembler.go`](/home/luis/Projects/AI/steiner/internal/prompt/assembler.go:13)
- supporting loaders in:
  - [`internal/prompt/context.go`](/home/luis/Projects/AI/steiner/internal/prompt/context.go:1)
  - [`internal/prompt/skills.go`](/home/luis/Projects/AI/steiner/internal/prompt/skills.go:1)
  - [`internal/prompt/system.go`](/home/luis/Projects/AI/steiner/internal/prompt/system.go:1)

Specific friction points:

- `Assembler.Assemble` directly appends sources in sequence ([assembler.go:59](/home/luis/Projects/AI/steiner/internal/prompt/assembler.go:59)).
- Budget clipping happens during block append, which mixes planning and rendering concerns ([assembler.go:32](/home/luis/Projects/AI/steiner/internal/prompt/assembler.go:32)).
- Conversation pass-through and tool summary placement are controlled by append order rather than an explicit plan ([assembler.go:137](/home/luis/Projects/AI/steiner/internal/prompt/assembler.go:137), [assembler.go:143](/home/luis/Projects/AI/steiner/internal/prompt/assembler.go:143)).
- Tests in [`internal/prompt/assemble_test.go`](/home/luis/Projects/AI/steiner/internal/prompt/assemble_test.go:15) rely heavily on positional message assertions, which is a signal that the missing interface is the ordered prompt-source plan.

The current module is not bad code, but the seam is too implicit. The architecture should make prompt-source decisions visible and testable without requiring many index-based assertions over final messages.

## Invariants to preserve

- Current source ordering should remain stable initially:
  - preamble
  - agents
  - project context
  - skills
  - durable context
  - conversation
  - tool summaries
- Conversation messages must continue to pass through unfiltered.
- Project context budget behavior must remain intact.
- Durable context rendering behavior must remain intact.
- Tool summaries must remain appended after conversation.
- Supporting loaders should remain in their owning modules:
  - agents loading
  - project context gathering
  - skill loading
  - system preamble construction
- Package boundaries from `AGENTS.md` must remain intact:
  - `internal/prompt` owns context assembly
  - `internal/tool` and `internal/provider` should not be pulled into prompt planning concerns

## Design decisions locked for execution

These decisions are settled for this subject and should not be re-litigated during implementation unless a concrete blocker appears.

### Decision 1: use a concrete planner module

Chosen direction:

- Use a concrete planner module inside `internal/prompt`, not a formal planner interface.

Reason:

- The problem is implicit planning, not missing substitutability.
- There is only one real planning strategy today.
- A formal interface would likely be shallow and speculative.

Implication for implementation:

- Prefer private concrete types/functions such as `planPromptSources(...)`.
- Avoid exported planner abstractions unless a real cross-package need appears.

### Decision 2: split planning from rendering

Chosen direction:

- Introduce an explicit internal plan representation.
- Plan first, render second.

Reason:

- The missing module is the ordered source plan itself.
- This separation improves locality and testability.

Implication for implementation:

- The executor should avoid a “just refactor append calls into more helpers” approach.
- The plan representation should stay concrete and small.

### Decision 3: preserve current ordering semantics first

Chosen direction:

- Keep current ordering behavior in the initial refactor.

Reason:

- This keeps the subject architectural rather than behavioral.
- It makes incremental verification much safer.

Implication for implementation:

- Stage 1 through Stage 6 should aim for behavior parity.
- Any rethinking of prompt ordering belongs in a later subject unless required for correctness.

### Decision 4: keep existing loaders as supporting modules

Chosen direction:

- `LoadAgents`, `GatherProjectContext`, `LoadSkillBlocks`, and `SystemPreamble` remain supporting modules.

Reason:

- They already have coherent responsibilities.
- The missing seam is the plan that coordinates them, not the existence of those helpers.

Implication for implementation:

- Do not absorb everything into a monolithic planner.
- The planner should orchestrate sources, not reimplement file-loading details.

### Decision 5: re-center tests on the plan and rendered result

Chosen direction:

- Add direct tests for the plan and keep end-to-end assembly tests for rendered parity.

Reason:

- Positional message assertions alone are too indirect for this seam.

Implication for implementation:

- Keep some final rendered-message assertions.
- Add plan-level assertions for order, inclusion, and budget outcomes.

## Proposed target design

Introduce a concrete planning pass inside `internal/prompt`.

Illustrative shape:

```go
package prompt

type plannedSourceKind string

const (
	plannedSourcePreamble       plannedSourceKind = "preamble"
	plannedSourceAgents         plannedSourceKind = "agents"
	plannedSourceProjectContext plannedSourceKind = "project_context"
	plannedSourceSkill          plannedSourceKind = "skill"
	plannedSourceDurableContext plannedSourceKind = "durable_context"
	plannedSourceConversation   plannedSourceKind = "conversation"
	plannedSourceToolSummary    plannedSourceKind = "tool_summary"
)

type plannedSource struct {
	Kind        plannedSourceKind
	Block       *ContextBlock
	Message     *provider.Message
	ApplyBudget bool
}

type promptPlan struct {
	Sources []plannedSource
}

func planPromptSources(ctx context.Context, opts AssemblyOptions, policy AssemblyPolicy) (promptPlan, error)
func renderPromptPlan(plan promptPlan, policy AssemblyPolicy) Assembly
```

This is illustrative only. The exact representation can differ, but the design intent should hold:

- concrete plan type
- explicit source kinds
- clear distinction between planning and rendering

Important constraints for the plan representation:

- keep it small
- avoid polymorphic tree structures
- avoid interface-heavy node designs
- prefer straightforward slices and structs

## Dependency model

The planner should depend on:

- `AssemblyOptions`
- normalized `AssemblyPolicy`
- existing loader helpers

The rendering step should depend on:

- `promptPlan`
- budget tracker / clipping behavior
- `blockMessage(...)`
- tool summary rendering

Prefer keeping the current `Assembler` entry point as the public orchestration surface while moving its internal ownership to:

- planning
- rendering

This lets callers keep using `Assemble(...)` while the internal seam gets deeper.

## Staging strategy

Implement in small stages. Each stage should compile, preserve behavior, and be safe to merge independently.

---

## Stage 1: Establish the Prompt Source Planning slot

### Goal

Create the architectural slot for **Prompt Source Planning** without changing behavior.

### Changes

- Add new internal files under `internal/prompt`, for example:
  - `source_plan.go`
  - optionally `source_plan_test.go`
- Introduce a concrete plan type and planned source kinds.
- Add an internal planning entry point and a rendering entry point.
- Initially let planning/rendering delegate back to existing assembler behavior if needed for parity.

### Deliverable

- New planning slot exists and compiles.
- No externally visible behavior change.

### Verification

- `gofmt -w internal/prompt/*.go`
- targeted compile or tests for `internal/prompt`

### Risks

- Avoid overdesigning the plan type before behavior actually moves.

---

## Stage 2: Make source ordering explicit in the plan

### Goal

Replace implicit append ordering with an explicit prompt-source plan.

### Changes

- Move source-order decisions from `Assembler.Assemble` into the planner:
  - preamble
  - agents
  - project context
  - skills
  - durable context
  - conversation
  - tool summaries
- The planner should describe order even if rendering still resembles current logic at first.
- Preserve current behavior exactly.

### Candidate files

- [`internal/prompt/assembler.go`](/home/luis/Projects/AI/steiner/internal/prompt/assembler.go:59)
- `internal/prompt/source_plan.go`

### Deliverable

- Source order is explicit and testable through the plan.

### Verification

- add plan-level tests asserting planned source kinds and order
- preserve existing end-to-end assembly tests

### Risks

- Do not accidentally reorder conversation or tool summaries while moving source ownership.

---

## Stage 3: Move budget application into the rendering phase

### Goal

Separate source planning from budget clipping and final block/message materialization.

### Changes

- Move clipping logic behind rendering rather than mixing it into planning.
- Revisit `assemblyState.appendBlock(...)` and related helpers ([assembler.go:32](/home/luis/Projects/AI/steiner/internal/prompt/assembler.go:32)).
- The planner decides what sources exist.
- The renderer decides how budgets clip block content and which blocks become messages.
- Conversation pass-through should remain unbudgeted.

### Candidate files

- [`internal/prompt/assembler.go`](/home/luis/Projects/AI/steiner/internal/prompt/assembler.go:18)
- `internal/prompt/source_plan.go`
- maybe a dedicated `source_render.go`

### Deliverable

- Budget behavior is applied in a rendering phase with a clear responsibility boundary.

### Verification

- preserve `TestGatherProjectContextHonorsBudget`
- add rendered-result tests showing budget clipping still works
- add plan tests showing planning is independent of clipping outcomes where appropriate

### Risks

- Be careful not to move budget logic into too many places. There should still be one obvious owner for clipping.

---

## Stage 4: Move conversation and tool-summary placement into the plan

### Goal

Make the distinction between block-backed sources and raw pass-through sources explicit.

### Changes

- Represent conversation pass-through in the plan explicitly.
- Represent tool-summary placement explicitly.
- Avoid encoding these decisions only in final append order.
- Keep tool-summary rendering behavior unchanged.

### Candidate files

- `internal/prompt/source_plan.go`
- [`internal/prompt/assembler.go`](/home/luis/Projects/AI/steiner/internal/prompt/assembler.go:137)

### Deliverable

- The plan explicitly accounts for conversation and tool-summary positions.

### Verification

- preserve `TestAssemblePassesFullConversationUnfiltered`
- add plan-level tests for conversation and tool-summary ordering

### Risks

- The plan representation should not become so generic that simple pass-through sources are harder to understand.

---

## Stage 5: Shrink Assembler into orchestration only

### Goal

Make `Assembler` the thin orchestrator over planning and rendering rather than the owner of append-by-append behavior.

### Changes

- Simplify `Assembler.Assemble(...)` so it:
  - constructs planning state
  - invokes the planner
  - invokes the renderer
  - returns final `Assembly`
- Remove or simplify append-oriented helpers once their responsibilities move.
- Keep remaining helper methods only if they still have a cohesive role.

### Candidate files

- [`internal/prompt/assembler.go`](/home/luis/Projects/AI/steiner/internal/prompt/assembler.go:59)
- new plan/render files

### Deliverable

- `Assembler` reads as orchestration over a deeper internal seam.

### Verification

- existing `internal/prompt` tests still pass
- add tests targeted at planner and renderer separately if useful

### Risks

- If `Assembler` remains large, that is a sign the planning/rendering split was not taken far enough.

---

## Stage 6: Re-center tests on the plan seam

### Goal

Test the real planning interface directly while preserving end-to-end assembly coverage.

### Changes

- Add direct plan tests in:
  - `internal/prompt/source_plan_test.go`
- Cover:
  - source order
  - source inclusion/exclusion
  - durable context placement
  - conversation placement
  - tool-summary placement
- Keep end-to-end rendered assertions in `assemble_test.go`, but rely less exclusively on positional message indexing.
- Where possible, replace brittle index tests with:
  - source-kind checks at plan level
  - smaller rendered-result assertions at assembly level

### Deliverable

- The real architecture seam has direct tests.

### Verification

- `go test ./internal/prompt`
- then broaden to `go test ./...`

### Risks

- Do not throw away end-to-end message assertions too aggressively; some still define important behavior.

---

## Stage 7: Cleanup and review

### Goal

Remove transitional duplication and confirm the planner is genuinely deep.

### Changes

- Delete transitional append helpers that no longer earn their keep.
- Re-check naming and file boundaries.
- Keep the plan representation concrete and compact.
- Ensure loaders remain supporting modules rather than being swallowed by the planner.

### Deletion test

Before closing the work, ask:

- If **Prompt Source Planning** were deleted, would source ordering, source inclusion, budget application decisions, and conversation/tool-summary placement reappear implicitly across the assembler and tests?
- If yes, the module is earning its keep.
- If no, the module is still shallow and needs another iteration.

### Final verification

- `gofmt -w` on touched Go files
- `go test ./internal/prompt`
- `go test ./...`
- optionally `go build ./...` if broader changes warrant it

### Expected outcome

- Better locality for prompt-source decisions
- A clearer seam between planning and rendering
- Less brittle prompt tests
- Safer future additions to prompt context sources
