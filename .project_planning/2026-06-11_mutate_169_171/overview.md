## Request

Plan one combined `mutate` work bundle covering GitHub issues `#169` and `#171` in `steiner`. The bundle should address the confirmed correctness bug in rollback result metadata, clarify and align batch semantics with actual behavior, add the missing text-deletion primitive as a new `delete_line` operation, and include the scoped API and UX improvements requested across both issues.

## Overview

The work is centered on `internal/tool/builtin/mutate*.go`, the `mutate` schema and result types, the adjacent tests, and the required documentation surfaces for built-in tools. The current implementation already applies same-file operations sequentially against evolving in-memory file state while validating `file_hash` against the initial on-disk snapshot. That behavior is workable, but the public schema/docs describe it imprecisely, and the current result payload does not expose enough information for callers to verify correctness without follow-up reads.

The plan should treat the two issues as one cohesive `mutate` API pass with three layers:

1. correctness fixes that make batch outcomes trustworthy
2. semantics and documentation alignment for existing behavior
3. additive API and UX improvements needed to close the gaps raised in both tickets

The highest-risk bug is from `#171`: a failed atomic batch leaves disk unchanged but still reports earlier operations as applied in `MutateResult`. That undermines caller trust in `modified`, `created`, `deleted`, `moved`, and `operations_applied`. Alongside that fix, the design should introduce a first-class `delete_line` text operation rather than changing `replace` semantics implicitly.

## Key Decisions

- Plan both issues in one bundle rather than splitting correctness and API cleanup.
  Rationale: the tickets overlap on semantics, docs, and result shape; planning them separately would duplicate design work and risk incompatible decisions.

- Skip external research.
  Rationale: the task is repo-local, the relevant behavior is discoverable from current code/tests/docs, and no external API or fast-moving dependency changes the implementation plan.

- Treat sequential in-memory batch application as the intended model.
  Rationale: the current code and issue `#171` both confirm this behavior; the problem is documentation ambiguity and weak result visibility, not a need to reverse the execution model.

- Introduce a standalone `delete_line` operation instead of changing `replace` by default or adding a `replace` flag.
  Rationale: line deletion is a distinct structured edit, and a new operation keeps the API explicit while avoiding heuristic text replacement behavior.

- Keep the planning scope constrained to the `mutate` surface and required docs.
  Rationale: the request is about tool behavior, schema, results, and documentation; unrelated agent/runtime refactors would add risk without helping these issues.

## Tradeoffs

- Standalone `delete_line` operation vs `replace` modifier.
  Chosen direction: standalone operation.
  Rejected for planning baseline: a `replace` flag such as `delete_line: true` because it overloads replacement semantics and leaves two concepts fused together.

- Single combined planning bundle vs multiple issue-specific bundles.
  Chosen direction: one bundle.
  Rejected for planning baseline: separate plans, because `#171` resolves the key ambiguity from `#169` and both tickets affect the same API and docs.

- Behavior change in default `replace` vs explicit new primitive.
  Chosen direction: explicit primitive.
  Rejected for planning baseline: making empty-string `replace` line-aware by default, because that would make byte substitution context-sensitive and harder to reason about.

- Tight correctness-first implementation vs a broader ergonomic API sweep.
  Chosen direction: include all requested issue items, but structure the work so correctness and semantics land before optional-looking UX additions.
  Deferred question: whether some additions, such as normalization or append synonyms, should be specified conservatively to avoid muddy overlap with existing operations.

## Scope Boundaries

In scope:

- `internal/tool/builtin/mutate*.go` and closely related schema/result code
- new `delete_line` operation design and implementation planning
- rollback metadata correctness for failed atomic batches
- documentation updates for actual batch semantics and new/changed tool behavior
- result payload improvements such as match counts and confirmation context
- post-condition assertions such as `assert_absent` and `assert_present`
- `file_hash` semantics for non-existent targets
- `move` overwrite semantics
- `insert_after` append-edge semantics
- nearby unit and functional tests needed to cover the new behavior
- required built-in tool documentation maintenance in `README.md`
- `docs/SUBAGENT_DELEGATION.md` only if the built-in tool descriptions there need updating because of the `mutate` API change

Out of scope:

- prompt, compaction, provider, or general agent-loop changes unrelated to `mutate`
- broad TUI or runtime refactors not required to support the `mutate` changes
- unrelated tool changes outside `mutate` and its immediate result/schema surfaces

## Verification Strategy

Repository guidance and CI both point to the same verification stack.

- `gofmt -w <files>`: cheap, required after Go edits
- `goimports -w <files>`: cheap, required after Go edits
- `go test ./internal/tool/builtin -run <TargetedTest>` or other targeted package tests: cheap to medium, preferred first-pass verification while iterating
- `go test ./...`: medium, broad functional/unit coverage
- `go test -race ./...`: expensive, repo-standard and present in CI
- `go build ./...` or `make build-binaries`: medium, build verification
- `go vet ./...`: medium
- `golangci-lint run ./...` or `make lint`: expensive, depends on installed tool
- `govulncheck ./...` or `make vuln`: expensive, depends on installed tool
- `make check`: expensive, repo-mandated final pre-closeout command; runs tidy, format/import checks, build, tests, race tests, vet, lint, and vuln checks

For execution and review, targeted tests should be run first around `internal/tool/builtin`, then broader checks as the change stabilizes, with `make check` required before finalizing Go implementation work.

## Decision Log

- 2026-06-11: User requested that issue `#171` be incorporated into the same planning analysis as `#169`.
- 2026-06-11: User confirmed the bundle should address everything on both tickets.
- 2026-06-11: User chose an explicit new `delete_line` operation over changing default `replace` behavior or adding a `replace` flag.
- 2026-06-11: External research was offered and explicitly declined; planning proceeds from repo-local evidence only.
