# Review: issue #536

## Scope and inputs

Reviewed the implemented diff against `origin/main`, `overview.md`, `plan.yaml`, and local `execution.md`. Scope covered delegation/advisor header metadata, compaction usage propagation and banner rendering, output-event conversion, tests, `docs/cache-stats.md`, and README cache-stat wording.

## Status

**pass_with_notes**

No blocking findings. Implementation meets approved behavior: model/reasoning precedes elapsed time in delegate and advisor headers; completed metadata orders status, model/effort, cache, elapsed; compaction banners render completed-request cache rate before elapsed when usage is available.

## Findings

### Blocking

None.

### Non-blocking

- **NB-001, accepted:** A compaction that performs normal then emergency summarization reports usage from final request only. `internal/agent/compaction.go` retains only final `CompactionOutcome.Usage`, so banner cache rate is not aggregate whole-compaction usage. Current docs accurately describe one banner request; reconsider if banners become whole-compaction metrics.
- **NB-002, accepted:** Cache-less providers with non-zero input render `cache 0.0%`. `internal/tui/content_render_compaction.go` uses `usagestats.HitRate`, and docs explicitly define this output. It may be read as cache support with zero hits.
- **NB-003, accepted:** No direct handler-level test covers finished compaction diagnostics with nil/all-zero usage. Existing guards and render omission tests lower risk; add coverage if this event path changes.
- **NB-004, accepted:** Additive output field `input_tokens` is summarizer non-cached input, while existing `prompt_tokens` is a post-compaction fit estimate. Downstream event consumers must not conflate them.

### Informational

- `compaction_escalation.go` was named in plan step 2 but correctly needed no edit; execution notes record the decision.
- `execution.md` is local ignored state while other plan artifacts are committed. This is a process consistency note, not product behavior.

## Verification

Passed:

- `go test ./internal/tui ./internal/agent ./internal/output`
- `go test -race ./internal/tui ./internal/agent ./internal/output`
- `go build ./...`
- `go vet ./...`
- `gofmt -l` on changed Go files
- `make check` with isolated `GOLANGCI_LINT_CACHE` (shared lint cache had stale paths from concurrent worktrees)

Tests cover header ordering and fallback, completed/in-progress compaction banner behavior, event-to-banner wiring, output conversion round trips, and summarizer usage propagation.

## Residual risks and closeout

Behavior risks are limited to the accepted notes above. Branch is local-only for engine closeout; no push or PR was performed in review.

## Advisor sanity check

Advisor recommends `pass_with_notes`. It confirmed no finding blocks closeout and called out the same residual risks: final-only usage for double-pass compaction, deliberate cache-less `0.0%`, the low-risk nil-usage handler coverage gap, and distinct token-field meanings.
