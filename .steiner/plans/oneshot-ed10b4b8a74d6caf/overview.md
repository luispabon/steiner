# Show model and cache metadata in tool headers

## Request

Update TUI delegate and advisor tool box headers to show the model and reasoning effort actually used. Running headers must show model/effort before time; completed headers must order status, model/effort, cache, then time. Add cache-hit-rate and time metadata to compaction banners.

## Overview

Use the model and reasoning fields already retained in `delegationDisplayState` to render metadata for active and terminal delegation boxes, including advisor boxes that use the same state. Preserve the existing right-aligned header layout and rely on the existing formatter when one runtime value is unavailable.

Carry compaction request usage from the summarizer response through agent diagnostics and output compatibility conversions into TUI banner state. Render its per-request cache hit rate before elapsed time, without changing the existing spinner or completion glyph and compaction count chrome. Cover rendering, event payload conversion, and usage propagation with focused tests, then update cache-stat documentation.

## Key Decisions

- D1: Reuse `delegationDisplayState.modelName` and `reasoning`; runtime event binding already resolves the actual selected model and effective reasoning setting, so no new delegation or provider event fields are needed.
- D2: Render model/effort with existing `formatModelEffort` whenever its result is non-empty, preserving its established fallback behavior when one runtime value is unavailable, plus current right alignment and styling conventions.
- D3: Terminal delegation metadata order is `status · model/effort? · cache? · elapsed?`; active metadata places model/effort before countdown or elapsed time. Apply the model/effort segment to all terminal headers that retain runtime display state, while preserving their existing status-specific fields.
- D4: Add cache-read, non-cached input, and cache-create token counts to both context diagnostics event shapes and their bidirectional conversion. Compute compaction cache rate with `usagestats.HitRate` using the established non-cached-input convention.
- D5: Preserve the existing compaction status glyph or spinner and trailing `#count`; insert optional `cache NN.N%` before elapsed time. The request changes informational metadata order, not unrelated banner chrome.
- D6: Document cache-rate surfacing because compaction usage becomes user-visible; do not change configuration, delegation policy, or prompt canon.

## Tradeoffs

- Showing configured aliases from static config was rejected because the issue requires values actually used and runtime state already has those values.
- Adding model/reasoning fields to completion events was rejected because completed boxes retain their live display state.
- Aggregating cache usage across all compactions was rejected. Each banner represents one summarizer request, so its own response usage gives an accurate local rate.
- Removing the compaction count was deferred because the issue does not request removal and it remains useful existing chrome.

## Scope Boundaries

In scope: TUI header rendering for delegate and advisor state, compaction usage propagation and rendering, focused tests, and cache-stat documentation.

Out of scope: changing model selection, reasoning capability resolution, provider usage collection, ordinary tool-call boxes, configuration, delegation tool policy, and prompt/oneshot internals.

## Verification Strategy

| Command | Cost | Safe-fix guidance |
|---|---:|---|
| `gofmt -w <changed-go-files>` and `goimports -w <changed-go-files>` | cheap | Preferred scoped formatter/import fix after Go edits. |
| `go test ./internal/tui ./internal/agent ./internal/output -run '<relevant tests>'` | low | Run focused package tests first; manual source fixes only. |
| `go test ./internal/tui ./internal/agent ./internal/output` | medium | Run after focused tests for affected packages. |
| `go build ./...` | low | Compile gate; no fix mode. |
| `make check` | high | Required final check. Runs tidy check, format/import checks, build, race tests, vet, lint, and vulnerability scan. Do not use broad automatic lint fixes. |

## Decision Log

- Research decision: external research was required because the task source is a live GitHub issue. The issue was retrieved and its requested header order was used as authoritative.
- Assumption A1: The title includes advisor boxes although the body only names sub-agent boxes. Advisor state shares the delegation renderer, so the same change covers it without a separate rendering path.
- Assumption A2: `MODEL/REASONING` uses existing `formatModelEffort` output. The issue does not specify placeholders, truncation, or alternate styling; that helper defines the fallback for a missing model or reasoning value.
- Assumption A3: `CACHE - TIME` for compaction means cache appears before elapsed time while preserving the current status glyph/spinner and `#count`.
- Related issue #522 may change advisor caching separately; this work only displays usage already delivered by each applicable event and does not add caching behavior.
- Advisor sanity check: applied dependency closure by making the plan serial; expanded terminal display coverage to all terminal headers retaining runtime state; corrected the formatter omission rule; and required advisor, terminal-status, and cache edge-case tests. Rejected scope expansion into model selection, provider usage collection, advisor caching, ordinary tool boxes, compaction-count removal, and new delegation completion event fields because existing runtime state and usage capture meet this request.
