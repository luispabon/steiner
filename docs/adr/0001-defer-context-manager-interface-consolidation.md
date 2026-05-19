# ADR-0001: Defer ContextManager and compaction deepening

## Status

Accepted — 2026-05-19

## Context

`internal/agent/context_manager.go` defines 12 single-method interfaces
(MutationRecorder, CompactionRecorder, EpochResetter, EventSinkSetter,
PreambleProvider, ToolResultIngestor, AssistantResponseIngestor,
ScaffoldInferrer, CompactionStrategyProvider, MaskingWindowProvider, plus
the 3-method ContextManager and Compactor). All are consumed via type
assertion on `ContextManager`, not as function parameters.

Two adapters exist: NaiveContextManager (classic accumulate-then-compact)
and SmartContextManager (experimental shaping with masking epochs,
scaffold inference, and scratchpad sync).

SmartContextManager is an active experiment. The alternative direction
under evaluation is reducing context bloat through delegation rather than
through ingestion-time shaping. SmartContextManager may be deleted.

`internal/agent/compaction.go` (947 LOC) contains three compactor
implementations: summarizeCompactor (classic, used by Naive), plus
dropCompactor and hybridCompactor (experimental, tied to Smart). Drop
compaction is related to tool-result masking. Compaction strategy
selection depends on the SmartContextManager type-asserted interfaces
(CompactionStrategyProvider, MaskingWindowProvider). The file is large
but hangs off a clean `Compactor.Compact` interface already.

## Decision

Do not consolidate the ContextManager interfaces or deepen compaction
into its own module until the Naive-vs-Smart experiment concludes. The
interface fragmentation and compaction strategy proliferation are both
direct consequences of the two-adapter split being exploratory.
Collapsing either now would couple callers to a design that may be
removed.

## Consequences

- The type-assertion pattern in runner.go, turn_progression.go,
  compaction.go, and tool_exec.go remains verbose but safe.
- compaction.go stays large (947 LOC) but coherent behind the
  Compactor interface. File-level splits (compactor per file) are
  fine as housekeeping but don't change the architecture.
- Future architecture reviews should not re-suggest interface
  consolidation or compaction deepening until SmartContextManager's
  fate is decided.
- If Smart is kept: consolidate into 2-3 role interfaces (ingestion,
  compaction coordination, scaffold) and deepen compaction behind a
  richer seam. If Smart is deleted: drop/hybrid compactors and most
  single-method interfaces disappear with it, and summarizeCompactor
  can inline into the turn progression path.
