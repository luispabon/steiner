# Architectural Review Subject 6

## Subject

Deepen the **Provider Request Execution** module in `internal/provider`.

## Summary

Refactor provider request handling so one deep module owns the full provider execution path with shared request setup and shared error semantics. Today, request payload shaping, streaming, non-streaming execution, wire decoding, and parts of provider-specific behavior are split across several files in ways that risk duplication and drift.

The target state is:

- External provider-facing methods remain stable initially.
- A concrete **Provider Request Execution** module owns:
  - request payload shaping
  - request construction
  - HTTP execution
  - response status handling
  - wire decoding
  - usage extraction
  - consistent error shaping
- Streaming and non-streaming remain internal variants under one request-execution architecture rather than separate architectural subjects.

This subject should be implemented incrementally and merged stage by stage.

## Current friction

Current logic is spread across:

- [`internal/provider/openai_compat.go`](/home/luis/Projects/AI/steiner/internal/provider/openai_compat.go:1)
- [`internal/provider/openai_stream.go`](/home/luis/Projects/AI/steiner/internal/provider/openai_stream.go:1)
- [`internal/provider/request_payload.go`](/home/luis/Projects/AI/steiner/internal/provider/request_payload.go:1)
- [`internal/provider/openai_wire.go`](/home/luis/Projects/AI/steiner/internal/provider/openai_wire.go:1)

Architecture friction likely appears in these forms:

- request payload shaping is separate from request execution, but the ownership boundary is not explicit
- streaming and non-streaming request paths risk semantic drift over time
- provider wire handling and provider request setup may be too dispersed to reason about as one module
- parity expectations between stream and non-stream responses are harder to test when common logic is not explicit

This subject should make the shared request-execution path the obvious internal seam.

## Invariants to preserve

- The external provider-facing methods should remain stable initially.
- OpenAI-compatible request semantics must remain intact.
- Shared request fields must remain aligned between streaming and non-streaming where applicable:
  - model
  - messages
  - tools
  - extra params
  - token-related request fields
- Streaming behavior must remain streaming from the caller’s perspective.
- Non-streaming behavior must remain non-streaming from the caller’s perspective.
- Usage extraction must remain correct for both response modes.
- Package boundaries from `AGENTS.md` must remain intact:
  - `internal/provider` owns model transport and scheduler-related provider behavior
  - higher-level loop and retry policy remain outside unless already provider-local

## Design decisions locked for execution

These decisions are settled for this subject and should not be re-litigated during implementation unless a concrete blocker appears.

### Decision 1: use a concrete Provider Request Execution module

Chosen direction:

- Use a concrete internal module, not a formal interface hierarchy.

Reason:

- The architecture problem is split ownership and semantic drift, not missing substitutability.
- There is one primary OpenAI-compatible request path today.

Implication for implementation:

- Prefer private concrete helpers or a private concrete request-execution type.
- Avoid exported provider execution interfaces.

### Decision 2: keep the external provider API stable initially

Chosen direction:

- Preserve the current provider-facing methods for now.

Reason:

- This limits blast radius into `internal/agent` and other callers.
- The subject is about internal deepening first.

Implication for implementation:

- Refactoring should happen behind the existing provider entry points.

### Decision 3: unify shared request setup and shared error handling

Chosen direction:

- Share request setup and common error handling between stream and non-stream paths.

Reason:

- This is the most likely source of drift if left split.
- It improves locality around payload and failure semantics.

Implication for implementation:

- Avoid duplicate request-building and duplicate status/error shaping logic.

### Decision 4: keep stream/non-stream as internal variants under one module

Chosen direction:

- Streaming and non-streaming are internal execution variants, not separate architectural subjects.

Reason:

- They share most of the request path.
- Splitting them at the architecture level now would likely produce shallow duplication.

Implication for implementation:

- Branch where wire behavior genuinely differs.
- Keep the shared path obvious.

### Decision 5: re-center tests on request execution behavior and parity

Chosen direction:

- Add tests that directly exercise shared request execution behavior and stream/non-stream parity.

Reason:

- The real seam is not just the existence of two methods, but the consistency of their shared semantics.

Implication for implementation:

- Preserve end-to-end coverage.
- Add more direct assertions for parity and common request behavior.

## Proposed target design

Keep the existing provider entry points, but deepen their internals around one concrete request-execution path.

Illustrative shape:

```go
package provider

import "context"

type requestExecutionInput struct {
	Request ChatRequest
	Stream  bool
}

type requestExecutionPlan struct {
	Payload []byte
	URL     string
}

func (p *openAICompatProvider) executeRequest(ctx context.Context, in requestExecutionInput) (any, error)
```

Illustrative internal phases:

- `buildRequestPayload(...)`
- `buildHTTPRequest(...)`
- `executeHTTP(...)`
- `decodeNonStreamResponse(...)`
- `decodeStreamResponse(...)`
- `shapeProviderError(...)`

This is illustrative only. The design intent should hold:

- one concrete request-execution path
- clear shared setup
- small branch points for stream vs non-stream wire handling

Avoid:

- one giant function with stream conditionals everywhere
- interface-heavy transport abstractions with no second adapter
- separate top-level architectures for stream and non-stream behavior

## Dependency model

The **Provider Request Execution** module should continue to use the existing provider state and configuration:

- base URL
- API key
- HTTP client
- model configuration

It should own:

- payload shaping handoff
- request construction
- shared headers and auth behavior
- shared status/error handling
- dispatch to stream/non-stream decoding

Prefer reusing existing provider structs rather than introducing a new public dependency surface.

## Staging strategy

Implement in small stages. Each stage should compile, preserve behavior, and be safe to merge independently.

---

## Stage 1: Establish the Provider Request Execution slot

### Goal

Create the architectural slot for **Provider Request Execution** without changing behavior.

### Changes

- Add new internal files under `internal/provider`, for example:
  - `request_execution.go`
  - optionally `request_execution_test.go`
- Introduce a concrete internal execution entry point.
- Have existing provider methods delegate into that entry point as minimally as possible.
- Initially allow the new entry point to forward into existing logic if needed for parity.

### Deliverable

- New request-execution slot exists and compiles.
- No externally visible behavior change.

### Verification

- `gofmt -w internal/provider/*.go`
- targeted compile or tests for `internal/provider`

### Risks

- Avoid overdesigning internal request-execution types before behavior moves.

---

## Stage 2: Centralize shared request setup

### Goal

Make request construction a shared module behavior across stream and non-stream paths.

### Changes

- Centralize:
  - payload construction handoff
  - request URL selection
  - headers/auth setup
  - common HTTP request construction
- Keep stream-specific differences explicit but downstream from shared setup.

### Candidate files

- [`internal/provider/request_payload.go`](/home/luis/Projects/AI/steiner/internal/provider/request_payload.go:1)
- [`internal/provider/openai_compat.go`](/home/luis/Projects/AI/steiner/internal/provider/openai_compat.go:1)
- [`internal/provider/request_execution.go`](/home/luis/Projects/AI/steiner/internal/provider/request_execution.go:1)

### Deliverable

- Shared request setup no longer risks duplication between stream and non-stream paths.

### Verification

- preserve existing request-payload tests
- add targeted tests for shared request construction behavior

### Risks

- Do not accidentally change request field parity while centralizing setup.

---

## Stage 3: Centralize shared HTTP execution and status/error handling

### Goal

Make transport execution and provider failure shaping part of one obvious module path.

### Changes

- Move common HTTP execution behavior into the new request-execution module.
- Centralize:
  - status-code handling
  - provider error body handling
  - common transport error shaping
- Keep stream/non-stream response decoding separate where appropriate.

### Candidate files

- [`internal/provider/openai_compat.go`](/home/luis/Projects/AI/steiner/internal/provider/openai_compat.go:1)
- [`internal/provider/openai_wire.go`](/home/luis/Projects/AI/steiner/internal/provider/openai_wire.go:1)
- [`internal/provider/request_execution.go`](/home/luis/Projects/AI/steiner/internal/provider/request_execution.go:1)

### Deliverable

- Shared HTTP execution and failure semantics are explicit and centralized.

### Verification

- preserve existing provider error tests
- add tests for shared status/error behavior

### Risks

- Be careful not to lose details from provider error payloads while unifying error shaping.

---

## Stage 4: Reframe non-stream decoding as an internal variant

### Goal

Keep non-stream response handling under the shared execution path with a clear decode branch.

### Changes

- Move or wrap non-stream-specific decode logic behind the shared execution path.
- Keep usage extraction and final response shaping intact.
- Ensure the branch point from shared execution into non-stream decode is obvious and small.

### Candidate files

- [`internal/provider/openai_compat.go`](/home/luis/Projects/AI/steiner/internal/provider/openai_compat.go:1)
- [`internal/provider/openai_wire.go`](/home/luis/Projects/AI/steiner/internal/provider/openai_wire.go:1)
- `internal/provider/request_execution.go`

### Deliverable

- Non-stream behavior becomes a well-defined variant inside **Provider Request Execution**.

### Verification

- preserve non-stream provider tests
- add targeted tests for:
  - successful decode
  - usage extraction
  - malformed response handling

### Risks

- Do not accidentally change how finish reason, message content, or usage are derived.

---

## Stage 5: Reframe stream decoding as an internal variant

### Goal

Keep stream handling under the same request-execution architecture without flattening its special behavior.

### Changes

- Move or wrap stream-specific behavior behind the shared execution path.
- Keep stream event parsing, chunk emission, completion detection, and usage extraction intact.
- Ensure the branch point from shared execution into stream decode is obvious and small.

### Candidate files

- [`internal/provider/openai_stream.go`](/home/luis/Projects/AI/steiner/internal/provider/openai_stream.go:1)
- [`internal/provider/openai_wire.go`](/home/luis/Projects/AI/steiner/internal/provider/openai_wire.go:1)
- `internal/provider/request_execution.go`

### Deliverable

- Stream behavior becomes a well-defined variant inside **Provider Request Execution**.

### Verification

- preserve streaming tests
- add targeted tests for:
  - chunk sequence handling
  - done event handling
  - usage extraction
  - malformed stream payload handling

### Risks

- Streaming code can easily become harder to read than before if the shared-path integration is too clever. Keep the branch points direct.

---

## Stage 6: Re-center tests on shared request behavior and parity

### Goal

Test the real seam directly: common request semantics with stream/non-stream variants.

### Changes

- Add direct tests in:
  - `internal/provider/request_execution_test.go`
- Cover:
  - shared request setup
  - shared status/error shaping
  - non-stream success/failure
  - stream success/failure
  - parity of shared request fields between modes
- Keep end-to-end provider tests, but reduce reliance on indirectly proving shared behavior through two separate paths.

### Deliverable

- The real architecture seam has direct tests.

### Verification

- `go test ./internal/provider`
- then broaden to `go test ./...`

### Risks

- Do not discard valuable end-to-end stream tests; parity testing complements them rather than replacing them.

---

## Stage 7: Cleanup and review

### Goal

Remove transitional duplication and confirm the module is genuinely deep.

### Changes

- Delete transitional wrappers or duplicate request-path helpers that no longer earn their keep.
- Re-check naming and file boundaries.
- Keep stream/non-stream branch points small and explicit.
- Avoid turning the provider package into a transport mini-framework.

### Deletion test

Before closing the work, ask:

- If **Provider Request Execution** were deleted, would payload shaping handoff, request setup, shared HTTP execution, stream/non-stream branching, and common error handling reappear across multiple provider files?
- If yes, the module is earning its keep.
- If no, the module is still shallow and needs another iteration.

### Final verification

- `gofmt -w` on touched Go files
- `go test ./internal/provider`
- `go test ./...`
- optionally `go build ./...` if broader changes warrant it

### Expected outcome

- Better locality for provider request behavior
- Less drift between stream and non-stream paths
- Clearer ownership of shared transport semantics
- Safer future provider changes
