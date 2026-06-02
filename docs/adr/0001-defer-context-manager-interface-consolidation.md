# ADR-0001: Context-manager consolidation was deferred during the smart-context experiment

## Status

Archived - 2026-06-02

## Context

This ADR existed to justify keeping the context-manager interface split while the smart-context experiment was still active. That experiment has now concluded, and that branch of the design is no longer part of the product direction.

The old rationale for deferring consolidation is therefore historical only. It should not be read as current guidance for the codebase.

## Decision

Treat this ADR as a record of the temporary deferral, not as an active architectural preference.

## Consequences

- Future context-manager cleanups should be evaluated against the current baseline pipeline, not against the retired smart-context experiment.
- Any remaining interface or compaction seams should be justified on present-day implementation needs.
- This document remains for historical traceability only.
