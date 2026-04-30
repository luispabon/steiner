# Slop Detector Report: cmd/

## Run Details

- **Date**: 2026-04-30 13:21
- **Base path**: `cmd/`
- **Mode**: `deep`
- **Files scanned**: 18
- **Files with findings**: 4
- **Total findings**: 5

## Severity Breakdown

| Severity | Count |
|----------|-------|
| High     | 1     |
| Medium   | 3     |
| Low      | 1     |

## Category Breakdown

| Category               | Count |
|------------------------|-------|
| Complexity reduction   | 0     |
| Extract method         | 1     |
| Language idioms        | 0     |
| Dead/unreachable code  | 1     |
| Naming                 | 0     |
| Signature hygiene      | 0     |
| Potential bugs         | 3     |

## Top Offenders

1. `cmd/steiner/interactive.go` — 2 findings. The interactive entrypoint is doing too much and also drops TUI runtime errors.
2. `cmd/steiner/commands.go` — 1 finding. Two subcommands build a runtime without closing it.
3. `cmd/steiner/approval.go` — 1 finding. Unused approval responder path remains in production code.
4. `cmd/glamour-test/main.go` — 1 finding. Final CLI output write ignores errors.

## Critical and High-Severity Findings

- `F001` — `cmd/steiner/commands.go` — `newToolsCommand / newSkillsCommand` — potential bugs — runtime cleanup is skipped, leaking closeable resources on successful command execution.

## Executor Handoff

This section is the operational handoff for an implementation agent. It is written to be sufficient on its own.

### Objective

Apply the five findings in `report.yaml` to the `cmd/` tree while preserving behaviour. The work is a cleanup/remediation pass, not a redesign. Keep package boundaries intact and keep changes local to `cmd/` unless a test or compiler error forces a broader change.

### Scope

- Primary code under change:
  - `cmd/steiner/commands.go`
  - `cmd/steiner/interactive.go`
  - `cmd/steiner/approval.go`
  - `cmd/glamour-test/main.go`
- Likely test files to update or add:
  - `cmd/steiner/commands_test.go`
  - `cmd/steiner/interactive_test.go`
  - possibly `cmd/steiner/main_test.go` if existing coverage is easier to extend there
- Do not change `internal/` package boundaries unless the current `cmd/` code cannot be tested otherwise.

### Recommended Execution Order

1. Fix `F001` in `cmd/steiner/commands.go`.
2. Fix `F005` in `cmd/glamour-test/main.go`.
3. Fix `F002` in `cmd/steiner/interactive.go`.
4. Refactor `F003` in `cmd/steiner/interactive.go`.
5. Remove dead code for `F004` in `cmd/steiner/approval.go`.

Reasoning:
- `F001` is the only high-severity issue and is self-contained.
- `F005` is a tiny isolated correctness fix.
- `F002` and `F003` touch the same function; handle the error-reporting fix before or during the extraction so the refactor does not preserve the bad behaviour by accident.
- `F004` is safest last, once the agent has confirmed no hidden call site depends on the dead responder.

### Per-Finding Remediation Instructions

#### `F001` — close runtimes in `tools` and `skills`

File:
- `cmd/steiner/commands.go`

Required change:
- In both `newToolsCommand` and `newSkillsCommand`, call `defer closeRuntime(&rt)` immediately after a successful `buildRuntime(...)`.

Why this is first:
- It fixes a real lifecycle leak and aligns these subcommands with the already-correct patterns in `runExecMode` and `runInteractiveMode`.

Expected tests:
- Add or update a test proving these commands close runtime resources.
- Best approach: stub `buildRuntime` to return a `cliRuntime` with a `closeFn` or closable test double, execute `tools` and `skills`, and assert the closer ran.

Success condition:
- Successful command execution still prints the same output.
- Runtime cleanup happens exactly once for each command.

#### `F005` — handle stdout write failure in `glamour-test`

File:
- `cmd/glamour-test/main.go`

Required change:
- Replace the bare `os.Stdout.Write([]byte(out))` with checked error handling.
- On write failure, print an error to stderr and exit non-zero.

Expected tests:
- If this helper currently has no tests, adding one is optional but useful if there is a lightweight way to exercise `main`.
- If testing `main` is awkward, the agent may choose to extract a tiny writer helper first, but keep the change minimal.

Success condition:
- Normal path is unchanged.
- Broken pipe / write failure no longer silently succeeds.

#### `F002` — stop swallowing TUI runtime failures

File:
- `cmd/steiner/interactive.go`

Required change:
- In the goroutine that runs `teaProgram.Run()`, stop discarding all errors.
- Emit a warning diagnostic event through `rt.events` when `teaProgram.Run()` returns an error.
- If there is a specific known benign quit error, suppress only that exact case, not all errors.

Expected tests:
- Extend `cmd/steiner/interactive_test.go` to verify the event sink receives a diagnostic event when the TUI runner reports an error.
- If direct Bubble Tea injection is difficult, introduce a narrow seam that allows the program runner to be stubbed. Keep the seam inside `cmd/steiner`.

Success condition:
- Normal quit still works.
- Unexpected TUI failures become visible in emitted diagnostics.

#### `F003` — split `runInteractiveMode`

File:
- `cmd/steiner/interactive.go`

Required change:
- Break up `runInteractiveMode` into smaller helpers while preserving current behaviour.
- Minimum useful extraction:
  - one helper for session/runtime/TUI setup
  - one helper for the main select loop / lifecycle
  - optionally one helper for manual compaction handling
- Preserve existing channel behaviour, event sink wrapping, history loading, and model switch behaviour.

Important constraints:
- Do not move ownership into `internal/tui`; the AGENTS guidance and `huh_boundary.go` make it clear that orchestration stays in `cmd/steiner`.
- Do not change the observable event stream unless required for `F002`.

Expected tests:
- Existing tests should continue to pass.
- Add focused unit tests only if the extraction introduces a new helper with non-trivial control flow.
- Do not create broad snapshot-style tests unless necessary.

Success condition:
- `runInteractiveMode` is materially smaller and easier to reason about.
- Behaviour and tests remain stable.

#### `F004` — remove dead `channelApprovalResponder`

File:
- `cmd/steiner/approval.go`

Required change:
- Delete `channelApprovalResponder` and its `RequestApproval` method.
- Confirm there are no references outside `cmd/steiner` before removal.

Expected tests:
- None required if there are truly no call sites and compilation passes.
- Re-run approval-related tests to ensure the remaining responders still cover the intended paths.

Success condition:
- The unused responder path is gone.
- No production or test code references it.

### Verification Plan

Run these in order after code changes:

1. `gofmt -w cmd/steiner/*.go cmd/glamour-test/main.go`
2. `go test ./cmd/steiner`
3. `go test ./...`

If the full suite is too slow or fails for unrelated reasons, still run at least:

- `go test ./cmd/steiner -run 'Test(Commands|Exec|Interactive|TUIApproval|Runtime)'`

Optional but useful:

- `go build ./cmd/steiner`
- `go build ./cmd/glamour-test`

### Behavioural Guardrails

- Preserve CLI output formats unless the finding explicitly requires new diagnostics on error.
- Preserve event emission semantics except where `F002` intentionally adds missing diagnostics.
- Keep approvals working in both exec mode and interactive mode.
- Avoid changing model selection, compaction semantics, history persistence, or tool registry behaviour except where touched directly by `F001` and `F003`.

### What Not To Do

- Do not broaden this into an `internal/` refactor.
- Do not add new configuration options.
- Do not remove existing tests just because the refactor makes them awkward.
- Do not replace the current architecture with a new abstraction layer unless a very small test seam is needed.

### Deliverable Expectation

When implementation is complete, the executing agent should report:

- which findings were fixed
- any findings intentionally deferred and why
- which tests were added or changed
- exact verification commands run and whether they passed

## Report Schema Reference

The skill’s referenced `report-schema.yaml` file was not present on disk, so this run used the concrete schema below. Executor agents should treat this as authoritative for this report.

### Top-level fields

- `version`: integer schema version for the report format.
- `run`: metadata about the audit run.
- `findings`: ordered list of actionable findings.

### `run` object

- `id`: unique run identifier, matching the artifact directory name.
- `generated_at`: human-readable timestamp for the report.
- `base_path`: root path that was crawled.
- `mode`: review mode used for the run. Values: `deep`, `triage`.
- `files_scanned`: total number of files reviewed after filtering.
- `files_with_findings`: number of files that contain at least one finding.
- `total_findings`: total number of findings in the report.

### `findings[]` object

- `id`: unique finding identifier. Format used here: `F001`, `F002`, and so on.
- `severity`: executor priority. Allowed values:
  - `high`: actively harmful or likely to cause bugs / hide failures.
  - `medium`: worth fixing soon, but not an immediate correctness break.
  - `low`: cleanup or polish with limited risk.
- `confidence`: reviewer confidence in the diagnosis. Allowed values:
  - `high`: issue and fix are both clear.
  - `medium`: issue appears real, but project context may affect the final shape of the fix.
- `category`: the primary review criterion that produced the finding. Values used in this run:
  - `complexity_reduction`
  - `extract_method`
  - `language_idioms`
  - `dead_unreachable_code`
  - `naming`
  - `signature_hygiene`
  - `potential_bugs`
- `file`: repository-relative file path to change.
- `language`: language identifier for that file.
- `symbol`: function, method, type, or logical code block owning the finding.
- `lines`: inclusive line location object:
  - `start`: first relevant line number.
  - `end`: last relevant line number.
- `summary`: one-sentence diagnosis.
- `why_it_matters`: user-facing impact or maintenance risk.
- `current_code`: minimal current excerpt needed to recognise the issue.
- `suggested_code`: ready-to-apply replacement or deletion guidance. Empty only when the action is pure deletion; when empty, the rationale must explain the deletion.
- `rationale`: why the suggested change is the right direction.
- `related_findings`: list of other finding IDs that should be considered together when sequencing fixes.
- `breaking_risk`: behaviour-change risk from applying the suggestion. Allowed values:
  - `low`: should preserve behaviour.
  - `medium`: likely safe, but needs targeted verification.
  - `high`: behaviour may intentionally or accidentally change.

### Ordering and execution notes

- Findings are ordered by file path, then by line number.
- `related_findings` should be resolved together when they point at the same control flow.
- The executor should use `current_code` to confirm the local context still matches before editing.
- The executor should update tests when a finding changes lifecycle or error-reporting behaviour.

## Notes

- No incomplete previous slop-detector run was found under `.project_planning/`.
- Most `cmd/` files were in reasonable shape; the report intentionally keeps the finding count low.
- The strongest issue is lifecycle symmetry: `runExecMode` and `runInteractiveMode` close runtime resources, but `tools` and `skills` commands currently do not.
