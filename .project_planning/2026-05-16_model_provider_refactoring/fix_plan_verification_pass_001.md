# Fix Plan — Verification Pass 001

## Trigger

`make quick-check` failed on `cl/2026-05-16_model_provider_refactoring` after merging `stage-7-step-1`.

## Failing Check

- `go test ./...`
- Failure: `cmd/steiner.TestModelInspectCommand`

## Observed Failure

The test expected `model inspect` output to report:

- `limits.source: fallback`
- fallback-derived output token defaults

Actual output reports:

- `limits.source: config`
- config/default-derived limits

This is consistent with current resolver behavior for a loaded config that already contains advanced limit defaults. The step contract does not require fallback for this case; it only requires that `model inspect` show the resolved state.

## Scope

Keep fixes limited to:

- `cmd/steiner/commands_test.go`
- any directly related stage-7 command tests if needed

Do not change implementation unless a real contract violation is discovered during review.

## Required Outcome

- Align the failing test with actual resolver semantics and the stage-7 contract.
- Preserve coverage for:
  - `model inspect` output shape
  - fallback warning behavior
  - metadata cache status/refresh/clear commands

## Verification

- `gofmt -w` on touched Go files
- `go test ./cmd/steiner/...`
- rerun `make quick-check`
