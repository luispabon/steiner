# Slop Detector Report: ./internal

## Run Details

- **Date**: 2026-04-30 14:11
- **Base path**: `./internal`
- **Mode**: `deep`
- **Files scanned**: 183
- **Files with findings**: 11
- **Total findings**: 12

## Severity Breakdown

| Severity | Count |
|----------|-------|
| High     | 2     |
| Medium   | 8     |
| Low      | 2     |

## Category Breakdown

| Category               | Count |
|------------------------|-------|
| Complexity reduction   | 0     |
| Extract method         | 3     |
| Language idioms        | 0     |
| Dead/unreachable code  | 1     |
| Naming                 | 0     |
| Signature hygiene      | 0     |
| Potential bugs         | 7     |

## Top Offenders

- **internal/tool/builtin/grep_core.go** - 2 findings. Search semantics have two hidden-failure paths: multiline mode is broken and filesystem errors are silently suppressed.
- **internal/config/load.go** - 1 finding. Configuration discovery can drift to process state and hide cwd/home resolution failures.
- **internal/config/validate.go** - 1 finding. One monolithic validator mixes unrelated rule sets and duplicates model checks.
- **internal/output/event_render.go** - 1 finding. Event formatting has grown into a large switch that should be split into per-event helpers.
- **internal/output/plain.go** - 1 finding. Streaming output swallows writer failures and keeps mutating state as if writes succeeded.
- **internal/prompt/assembler.go** - 1 finding. Prompt assembly encodes a long order-sensitive pipeline inline.
- **internal/tool/builtin/decode.go** - 1 finding. Numeric coercion silently truncates float inputs when integer fields are expected.
- **internal/tool/executor.go** - 1 finding. Approval backend failures bypass the executor’s structured error type.
- **internal/tui/git.go** - 1 finding. Git-state bootstrapping hides getwd failures and silently disables repo status.
- **internal/tui/model.go** - 1 finding. Package-level error hooks capture a specific model instance and can bleed across instances.

## High-Severity Findings

- **F003** - `internal/tool/builtin/decode.go`, `setField`, `potential_bug`: Float-to-integer coercion silently truncates or wraps malformed numeric tool input instead of rejecting it.
- **F004** - `internal/tool/builtin/grep_core.go`, `grepSearch`, `potential_bug`: The `multiline` flag never matches across newline boundaries because the search still runs line by line.

## Executor Instructions

This section is the entry point for an executor agent. The companion file `report.yaml` in this directory contains every finding with full detail. Read this section in full before beginning work.

### What report.yaml contains

- `id` - unique identifier (`F001`, `F002`, ...)
- `file` - relative path to the source file
- `language` - the file language
- `method` - the function or method the finding applies to
- `enclosing` - the containing class, struct, module, or receiver type (`null` when free-standing)
- `line_range` - original `[start, end]` source lines at scan time
- `category` - one of `complexity_reduction`, `extract_method`, `language_idioms`, `dead_code`, `naming`, `signature_hygiene`, `potential_bug`
- `severity` - `high`, `medium`, or `low`
- `confidence` - `high` or `medium`
- `description` - the problem and why it matters
- `current_code` - the exact source snippet that needs changing
- `suggested_code` - the proposed replacement, ready to paste. Empty means informational-only.
- `rationale` - one sentence on why the replacement is better
- `breaking_risk` - `low`, `medium`, or `high`
- `related_findings` - IDs of other findings that interact with this one

### Processing order

1. Process findings grouped by file.
2. Within each file, apply findings in **reverse line order** (highest `line_range` first).
3. Prefer high-severity findings before medium, and medium before low, when choosing which file to tackle next.

### How to apply each finding

1. Open the file named by `file`.
2. Locate `current_code` by content matching rather than line number alone.
3. Skip any finding whose `suggested_code` is empty.
4. Replace the matched `current_code` with `suggested_code`, adjusting indentation only if required by surrounding context.
5. Check `related_findings` before applying overlapping changes.

### Verification and commits

- After applying all findings in one file, verify the project still builds or compiles.
- For `breaking_risk: medium` or `breaking_risk: high`, run the project test suite after applying and before committing.
- Commit after each file is complete with `refactor(<scope>): apply slop-detector findings F001-F00N`.
- If `current_code` cannot be found because the file already moved on, skip the finding and note it.

### What NOT to do

- Do not re-analyse the code or second-guess findings.
- Do not apply findings with empty `suggested_code`.
- Do not modify code beyond what `suggested_code` specifies.
- Do not keep a change that breaks the build or fails tests.

## Report Schema Reference

- `id` - string. Format `F001`, `F002`, sequential across the report.
- `file` - string. Relative path from repo root.
- `language` - string. Lowercase language name.
- `method` - string. Function or method name; use `<top-level>`, `<file-level>`, constructor name, or `<anonymous:lineN>` when needed.
- `enclosing` - string or null. The containing class, struct, module, namespace, or Go receiver type.
- `line_range` - two-element integer array `[start, end]`, 1-indexed and inclusive, referring to the file on disk at scan time.
- `category` - one of `complexity_reduction`, `extract_method`, `language_idioms`, `dead_code`, `naming`, `signature_hygiene`, `potential_bug`.
- `severity` - one of `high`, `medium`, `low`.
- `confidence` - one of `high`, `medium`.
- `description` - 2-5 sentences explaining the specific problem and why it matters.
- `current_code` - exact verbatim source snippet that needs changing, with enough context to stand alone.
- `suggested_code` - complete replacement, ready to paste. Empty string only for catalogue-only informational findings.
- `rationale` - one sentence describing the concrete improvement.
- `breaking_risk` - one of `low`, `medium`, `high`.
- `related_findings` - array of finding IDs that interact with this one; empty array when independent.

## Notes

- No generated-code markers were found under `internal/`; the scan covered hand-written package code and adjacent package tests.
- The only high-severity behavior bugs remain the non-functional multiline grep path and lossy float-to-integer input coercion.
- This revised report uses the lower threshold you requested, so it now includes design-debt and contract-ambiguity findings that were intentionally omitted from the stricter first pass.
- The repo has no `golangci-lint` configuration at the root, so several maintainability issues currently rely on review discipline rather than automated enforcement.
