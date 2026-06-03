## Request

Fix mutate tool hash validation and operation reporting bugs (GitHub issue #110).

Multi-operation mutate calls on the same file fail because `file_hash` is verified against in-progress state rather than the original disk snapshot. Operation counts are misleading on partial failure, and skipped operations are unreported.

## Overview

Three bugs in `internal/tool/builtin/mutate.go`:

### Bug 1 — Hash verification checks in-progress state, not disk snapshot

`verifyFileHash()` (mutate.go:124) computes hash from `state.content` — the in-memory working copy modified by prior operations in the same batch. When op 1 modifies file X, op 2's hash check fails because `state.content` no longer matches the original hash the caller passed from `read`/`grep`.

Fix: verify against `state.original` (content loaded from disk at first access), not `state.content`. All operations in a single mutate call should validate against the same disk snapshot.

### Bug 2 — Misleading `operations_applied` on partial failure

`p.applied` increments after each successfully planned (in-memory) operation. On failure mid-batch, `commit()` never runs — nothing is written to disk. But the result reports `operations_applied: N` where N > 0, making the caller believe some operations succeeded and the file changed. The caller then re-reads and gets the "same" hash, concluding incorrectly that the read tool returns stale data.

Fix: `operations_applied` should only count operations planned, and a new `operations_skipped` field should report how many were never attempted. The existing field name is acceptable since the schema description says "All operations are planned before any filesystem writes are committed" — the caller should understand that partial failure means nothing was committed. But adding `operations_skipped` makes the gap explicit.

### Bug 3 — No per-operation status reporting

When op N fails, ops N+1..end are silently skipped. The caller gets one error message and `operations_failed: 1` regardless of how many operations were never attempted.

Fix: add `OperationsSkipped int` to `MutateResult`. On failure at operation N out of M total: `operations_applied = N-1`, `operations_failed = 1`, `operations_skipped = M - N`.

### Non-issues

- **Read tool caching (feedback point 3 from issue):** `read.go` does a fresh `os.ReadFile` each call. No caching. The "stale hash" was caused by Bug 2 — the file was never written to disk, so re-reading returns the original hash. This is expected behavior; fixing Bug 2's reporting eliminates the confusion.

### Files affected

- `internal/tool/builtin/mutate.go` — hash verification fix, operation counting fix
- `internal/tool/builtin/result.go` — add `OperationsSkipped` field
- `internal/tool/builtin/mutate_test.go` — tests for all three fixes
- `internal/tool/builtin/schema.go` — update `file_hash` description to clarify batch semantics

## Verification Strategy

| Check | Command | Cost |
|-------|---------|------|
| Format | `gofmt -w <files>` | cheap |
| Imports | `goimports -w <files>` | cheap |
| Targeted tests | `go test ./internal/tool/builtin/ -run TestMutate` | cheap |
| All builtin tests | `go test ./internal/tool/builtin/` | cheap |
| Full test suite | `go test ./...` | medium |
| Vet | `go vet ./...` | cheap |
| Build | `go build ./...` | medium |
| Full check | `make check` | medium |

Run targeted tests first, then `make check` at the end.

## Decision Log

- **Hash check target:** Use `state.original` not `state.content`. Considered "only check on first touch" but that would silently skip hash validation on subsequent operations, which could mask external changes between a read and the mutate call if the first operation targets a different file. Checking against original is both simpler and more correct.
- **Operation counting:** Keep `operations_applied` as "planned in memory" since the schema already documents plan-then-commit semantics. Add `operations_skipped` rather than changing the meaning of existing fields, to avoid breaking consumers.
- **No auto-heal:** Issue suggested "if hash mismatches but old_string matches, apply anyway with warning." Not implementing — hash mismatch after the fix will only happen from genuine external changes, where auto-healing could mask real conflicts. The fix eliminates the false-positive case that motivated this suggestion.
- **No read tool changes:** Read tool has no caching bug. The confusion was caused by mutate's misleading reporting.
