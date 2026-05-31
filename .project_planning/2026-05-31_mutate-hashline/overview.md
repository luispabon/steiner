## Request

Adapt key ideas from the "hashline" edit format (blog.can.ac/2026/02/12/the-harness-problem/) to improve steiner's mutate tool. The hashline approach tags read output with per-file content hashes and uses line-number anchored edits, reducing output tokens and catching stale-file edits before they corrupt.

## Overview

Three changes, all additive and backward-compatible:

### 1. File-hash staleness guard

Add an optional `file_hash` field to `MutateOperation`. When provided, the planner verifies the hash matches the current on-disk content before applying that operation. On mismatch, the operation fails with a diagnostic telling the model to re-read.

- Hash: CRC32 (IEEE) of content after normalizing trailing whitespace per line, low 16 bits, formatted as 4 uppercase hex chars (e.g. `"A1B2"`). 65,536-value space — a fingerprint, not cryptographic. Collisions are acceptable; the guard catches the common case (stale line numbers after another tool modified the file), not adversarial tampering.
- No new Go dependencies (stdlib `hash/crc32`).
- `file_hash` is optional on all operation types. Omitting it preserves current behavior exactly.
- Operations that don't reference an existing file (`create` with a new path) skip verification even if `file_hash` is provided.
- The hash covers the file as the planner sees it at that operation's turn — so if operation 1 modifies file A and operation 2 also targets file A, operation 2's `file_hash` would need to match operation 1's result, not the original disk content. This matches steiner's existing "operations see prior edits" semantics.

### 2. Hash-tagged read/grep output

Add `file_hash` to `ReadResult` and `GrepResult` structs. The read and grep tools compute the hash of the file content they return and include it in the result. The model sees this hash in tool results and can pass it back to mutate.

- Read: `ReadResult.FileHash` field, always populated.
- Grep: `GrepResult.FileHashes` field — a map of `path → hash` for every file that appears in results.
- Minimal input-token overhead: 4 hex chars per file, not per line.
- The hash function is shared (`internal/tool/builtin` package-level helper).

### 3. Insert operations

Add `insert_before` and `insert_after` operation types. These use a line-number anchor and add content without replacing anything.

- `insert_before`: insert `content` before line N. Existing content shifts down.
- `insert_after`: insert `content` after line N. Use line 0 with `insert_after` to prepend to file start (or `insert_before` line 1).
- `line` field is required (1-based). `content` field provides the text to insert.
- Like `line_replace`, these operate on text files only (binary check applies).
- `file_hash` is supported on insert operations.
- Token savings: model writes only new content, never reproduces old content for anchoring. This is the highest-leverage output-token reduction.

### What we are NOT doing

- **DSL string input**: Steiner's structured JSON schema is a strength — it enables validation, policy enforcement, and approval previews at the JSON level. A DSL string would bypass all of that.
- **Per-line content hashes**: Oh-my-pi's actual implementation uses per-file hash, not per-line. Per-line would add significant input-token overhead for marginal benefit.
- **Snapshot store / 3-way merge recovery**: Complex, high risk, and steiner's simpler "reject and re-read" is sufficient for a local-first agent.
- **`replace block` / `delete block`**: Requires tree-sitter integration, out of scope.
- **Changing line-number semantics**: Steiner's "operations see prior edits" is already tested and intuitive for sequential thinking. Hashline's "original-file coordinates" has tradeoffs that don't suit steiner's model.

### Files touched

- `internal/tool/builtin/mutate.go` — file hash verification, insert operations
- `internal/tool/builtin/schema.go` — schema updates for `file_hash`, `insert_before`, `insert_after`
- `internal/tool/builtin/input.go` — `FileHash` field on `MutateOperation`
- `internal/tool/builtin/result.go` — `FileHash` on `ReadResult`, `FileHashes` on `GrepResult`
- `internal/tool/builtin/read.go` — compute and return file hash
- `internal/tool/builtin/grep.go` — compute and return file hashes
- `internal/tool/builtin/file_hash.go` — new file, shared hash computation
- `internal/tool/builtin/mutate_test.go` — new test cases for hash verification, insert operations
- `internal/tool/builtin/file_hash_test.go` — new file, hash computation tests
- `internal/tool/builtin/read_test.go` — verify file_hash in read results
- `internal/tool/builtin/grep_test.go` — verify file_hashes in grep results

### Risk assessment

- **Low risk**: All changes are additive. `file_hash` is optional. Insert operations are new types that don't modify existing operation logic. Existing tests must pass unchanged.
- **Medium concern**: Hash function choice must be deterministic across platforms. CRC32-IEEE is stable in Go stdlib.
- **Edge case attention**: Files with no trailing newline, CRLF files, empty files, binary files, files modified between operations in the same mutate call.

## Verification Strategy

### Commands (from CLAUDE.md and repo)

| Command | Cost | Notes |
|---------|------|-------|
| `gofmt -w <files>` | cheap | format after edits |
| `goimports -w <files>` | cheap | fix imports |
| `go build ./...` | medium | compilation check |
| `go vet ./...` | medium | static analysis |
| `go test ./internal/tool/builtin/... -run <pattern>` | cheap | targeted test |
| `go test ./...` | medium | full suite |
| `go test -race ./...` | expensive | race detector |
| `golangci-lint run ./...` | medium | linter |
| `make check` | expensive | full pipeline |

### Test plan

**Zero-regression gate**: All 25+ existing mutate/diagnostics/diff test functions must pass unchanged before any new tests are added.

**New test coverage for file_hash**:
- Valid hash accepted — operation proceeds
- Stale/wrong hash rejected — operation fails with diagnostic message
- Missing hash (omitted) — backward compat, operation proceeds without verification
- Hash of empty file
- Hash of file with no trailing newline
- Hash of CRLF file
- Hash after prior operation modified the same file (intra-call staleness)
- Hash on create operation (should be ignored — file doesn't exist yet)
- Hash on delete operation
- Hash on move operation (verified against source file)
- Hash normalization: trailing whitespace stripped before hashing

**New test coverage for insert operations**:
- `insert_before` at line 1 (prepend)
- `insert_before` at middle line
- `insert_before` at last line
- `insert_after` at line 1
- `insert_after` at last line (append)
- `insert_after` at line 0 (error or prepend — decide during implementation)
- Insert single line
- Insert multi-line content
- Insert into single-line file
- Insert into empty file (error — no valid line to anchor)
- Insert with content that has trailing newline vs without
- Insert preserves CRLF line endings
- Insert on binary file (rejected)
- Insert on nonexistent file (rejected)
- Insert combined with other operations on same file
- Insert with `file_hash` verification

**New test coverage for read/grep hash output**:
- Read result includes `file_hash` field
- Read result hash matches expected computation
- Grep result includes `file_hashes` map
- Hash is deterministic across repeated reads of same content

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | CRC32-IEEE, low 16 bits, 4 hex chars | Stdlib, no dependency. Same collision space as oh-my-pi's xxHash32 approach. Fast. |
| 2 | Per-file hash, not per-line | Oh-my-pi's actual implementation is per-file. Per-line adds token overhead for marginal gain. |
| 3 | `file_hash` optional on all operations | Backward compatibility. Models that don't use it get identical behavior. |
| 4 | Keep "operations see prior edits" semantics | Already tested, intuitive for sequential multi-operation calls. Changing would break existing tests and model expectations. |
| 5 | Normalize trailing whitespace before hashing | Prevents spurious mismatches from editor whitespace handling. Matches oh-my-pi's approach. |
| 6 | Insert operations as new types, not line_replace overload | Cleaner schema, clearer intent, easier to test independently. |
| 7 | No snapshot store or recovery | Steiner is local-first. "Reject and re-read" is simple, safe, and sufficient. |
| 8 | Shared hash helper in `file_hash.go` | Used by read, grep, and mutate. Single source of truth for the algorithm. |
