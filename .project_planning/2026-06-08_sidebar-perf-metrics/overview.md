## Request

GitHub issue #138: Display model call performance metrics in the TUI sidebar — total request duration, time to first token (TTFT), and output tokens per second. Show metrics for the latest model call only.

## Overview

Timing data is not currently captured anywhere in the provider or agent layers. The `ModelCallFinishedEvent` carries token counts and finish reason but no timing. The sidebar already renders model, context, skill, and repository sections but has no performance section.

The plan adds timing capture at the model-call boundary in `internal/agent/turn_progression.go`, extends `ModelCallFinishedEvent` with three new fields (duration, TTFT, output tokens/sec), and adds a "performance" sidebar section that updates on each `ModelCallFinishedEvent`.

**Key design decisions:**

1. **Capture point:** Timing is captured in `executeModelCall` (turn_progression.go), wrapping the `performModelCall` call. This captures the full round-trip including streaming. TTFT comes from the first content/thinking chunk timestamp — tracked via a new `firstChunkTime` field threaded through `consumeModelStream`.

2. **Propagation:** Extend `ModelCallFinishedEvent` with `DurationMs int64`, `TTFTMs int64`, and `OutputTPS float64`. The event constructor gains matching parameters. No new event type needed.

3. **Display:** New `performanceSection()` in `sidebar_sections.go`, shown between the model and context sections. Three fields: `duration`, `ttft`, `tps`. Values display as `—` until the first model call completes.

4. **Scope:** Latest call only. Each `ModelCallFinishedEvent` overwrites the previous metrics. No accumulation.

## Verification Strategy

| Command | Cost | Notes |
|---------|------|-------|
| `gofmt -w <files>` | cheap | auto-fix |
| `goimports -w <files>` | cheap | auto-fix |
| `go build ./...` | cheap | |
| `go test ./internal/agent/... ./internal/output/... ./internal/tui/...` | medium | targeted |
| `go test ./...` | medium | full suite |
| `go vet ./...` | cheap | |
| `golangci-lint run ./...` | medium | |
| `make check` | medium | runs all of the above |

Run `make check` as final gate. Manual verification: run steiner interactively against a local model, observe the sidebar updating after each model response.

## Decision Log

- **Latest call only** — user confirmed; no accumulation across turns.
- **Extend existing event** — no new event type; `ModelCallFinishedEvent` is the natural place since it already marks the end of a model call.
- **TTFT via chunk tracking** — the streaming path emits chunks through `consumeModelStream`; capture `time.Now()` on first content/thinking chunk. For non-streaming (ChatCompletion), TTFT equals total duration since the full response arrives at once.
- **No research needed** — purely repo-local, all patterns understood from code exploration.
