---
name: simplify
description: Analyze branch changes for code quality improvements across reuse, simplification, efficiency, and altitude categories using parallel sub-agents. Produces a report and fix plan, then runs an approved fix/review loop. Use on a feature branch to clean up before review.
---

# Code Simplification Analysis

## Overview

Use this skill on a feature branch to analyze changes for structural and quality improvements. The workflow analyzes changed files against four quality categories (Reuse, Simplification, Efficiency, Altitude) using parallel explore sub-agents, aggregates findings through an advisor sanity check, and presents a report to the user. If fixes are approved, the skill enters a fix/review loop with optional verification reruns. This skill does NOT hunt for bugs — that is `/review`'s domain. All proposed changes must preserve existing behavior.

## Input Contract

- Accept an optional argument for the base branch (default: `main`).
- Require the current branch to differ from the base branch — error if already on main.
- No planning artifacts required — this skill operates directly on the branch diff.

## Analysis Flow

Follow this sequence:

1. Determine base branch (argument or `main`).
2. Compute changed files: `git diff <base>..HEAD --name-only` plus `git diff --name-only` for uncommitted changes. Deduplicate the combined list.
3. If no changed files, report "nothing to analyze" and stop.
4. Dispatch four parallel `explore` sub-agents, one per category. Each receives: the list of changed files, the base branch name, and the category-specific analysis prompt from ## Category Prompts. Each agent must return findings as a structured list with: finding ID (category prefix + number, e.g. R1, S2, E3, A1), severity (blocking/non_blocking/informational), file path and line range, description, and suggested fix.
5. Aggregate all four reports into a unified findings list.
6. Call the advisor with the aggregated findings for a sanity check. Incorporate feedback: drop findings the advisor flags as weak or incorrect, adjust severity per advisor guidance, add concerns the advisor raises that sub-agents missed. The advisor sees the findings and a summary of changed files — not the full code.
7. Present the refined report to the user, organized by category (Reuse, Simplification, Efficiency, Altitude), with finding counts and severity breakdown.

## Category Prompts

### Reuse

You are analyzing code changes for opportunities to reduce duplication and increase reuse. Review the changed files listed below and identify findings in these areas:

- Duplicated logic within or across changed files
- Code that could be extracted into a shared helper
- Dead code introduced or left behind by the changes
- Opportunities to use existing utilities or patterns already present in the codebase (must read nearby related files to find existing patterns)

**Changed files:** (passed as list)

**Base branch:** (passed as variable — substitute the base branch name)

**Behavior preservation constraint:** Your analysis must focus on structural changes only. Do not propose changes that would alter existing behavior. Bug fixes and functional changes are out of scope.

**Instructions:**

- Use the `read` tool to examine changed files.
- Check surrounding context: imports, call sites, package structure, related files in the same directory, and referenced packages. This grounds findings in evidence.
- For each finding, produce: finding ID (R1, R2, ...), severity (blocking/non_blocking/informational), file:line range, description, and suggested fix.

### Simplification

You are analyzing code changes for opportunities to improve clarity and reduce structural complexity. Review the changed files listed below and identify findings in these areas:

- Naming clarity (variables, functions, types that could be clearer)
- Structural improvements (unnecessary nesting, complex conditionals that could be simplified)
- Overly verbose constructs that have simpler idiomatic equivalents
- Unnecessary abstraction layers added by the changes

**Changed files:** (passed as list)

**Base branch:** (passed as variable — substitute the base branch name)

**Behavior preservation constraint:** Your analysis must focus on structural changes only. Do not propose changes that would alter existing behavior. Bug fixes and functional changes are out of scope.

**Instructions:**

- Use the `read` tool to examine changed files.
- Check surrounding context: imports, call sites, package structure, related files in the same directory, and referenced packages. This grounds findings in evidence.
- For each finding, produce: finding ID (S1, S2, ...), severity (blocking/non_blocking/informational), file:line range, description, and suggested fix.

### Efficiency

You are analyzing code changes for runtime and resource efficiency improvements. Review the changed files listed below and identify findings in these areas:

- Unnecessary allocations (slices, maps, strings built in loops)
- Wasteful iteration patterns (multiple passes where one suffices)
- Hot-path inefficiencies (work done repeatedly that could be cached or hoisted)
- Unnecessary I/O or syscalls

**Changed files:** (passed as list)

**Base branch:** (passed as variable — substitute the base branch name)

**Behavior preservation constraint:** Your analysis must focus on structural changes only. Do not propose changes that would alter existing behavior. Bug fixes and functional changes are out of scope.

**Instructions:**

- Use the `read` tool to examine changed files.
- Check surrounding context: imports, call sites, package structure, related files in the same directory, and referenced packages. This grounds findings in evidence.
- For each finding, produce: finding ID (E1, E2, ...), severity (blocking/non_blocking/informational), file:line range, description, and suggested fix.

### Altitude

You are analyzing code changes for correct abstraction level and package boundary adherence. Review the changed files listed below and identify findings in these areas:

- Logic placed at the wrong abstraction level (e.g. business logic in a handler, presentation logic in a domain package)
- Concerns mixed across layers (e.g. config parsing interleaved with validation)
- Changes that should be higher-level (policy) but are implemented as low-level (mechanism) or vice versa
- Package boundary violations where code reaches into another package's internals

**Changed files:** (passed as list)

**Base branch:** (passed as variable — substitute the base branch name)

**Behavior preservation constraint:** Your analysis must focus on structural changes only. Do not propose changes that would alter existing behavior. Bug fixes and functional changes are out of scope.

**Instructions:**

- Use the `read` tool to examine changed files.
- Check surrounding context: imports, call sites, package structure, related files in the same directory, and referenced packages. This grounds findings in evidence.
- For each finding, produce: finding ID (A1, A2, ...), severity (blocking/non_blocking/informational), file:line range, description, and suggested fix.

## Findings

Classify findings as:

- `blocking`: must be fixed before completion
- `non_blocking`: record but does not block completion
- `informational`: useful note only

Map final status as:

- `fail` if blocking findings remain
- `pass_with_notes` if no blocking findings remain but non-blocking findings remain
- `pass` if only informational findings or no findings remain

## Fix Plan

If blocking or non_blocking findings exist:

1. Produce a consolidated fix plan mapping each fix to its finding ID(s) and stating which verification will be rerun.
2. Present the fix plan to the user and ask for explicit confirmation.
3. If the user declines, report findings and stop. No code is modified.
4. If the user approves, enter the fix/review loop.

## Fix/Review Loop

Mirror the review skill's fix loop exactly:

- Same delegation tier preference: isolated (worktree) > direct (code sub-agent) > inline (last resort)
- Worktrees created under `.steiner/worktrees/` (not /tmp)
- Worktree provisioning: after `git worktree add`, verify with `ls -d <path>` and `git -C <path> branch --show-current`; if either fails, prune with `git worktree remove` and fall back to direct delegation
- Same pre-commit checklist (isolated mode: check branch name, worktree path, git status scope; direct mode: check branch name, git status scope)
- Fix work is sequential, not parallel
- After fixes, run verification (scoped checks or `make check`)
- Lightweight re-review: check whether fixes introduced new quality issues in the same four categories. Repeat only if new blocking findings emerge. Cap at 2 fix iterations.

### Worktree Provisioning

Always create worktrees under `.steiner/worktrees/` inside the project root. Do not use `/tmp` or other system temporary directories — they may be sandboxed and silently fail.

After running `git worktree add`, verify the directory actually exists:

1. Run `ls -d <worktree-path>` to confirm the directory was created.
2. Run `git -C <worktree-path> branch --show-current` to confirm it is on the expected temporary branch.
3. If either check fails, prune the worktree entry with `git worktree remove <worktree-path>` and fall back to direct delegation.

### Fix/Review Delegation

The fix delegated agent must:

- receive only the approved findings, fix plan, relevant files, constraints, and verification strategy
- run the pre-commit checklist before committing (see below)
- commit its changes on the working branch (temporary branch for isolated delegation, feature branch for direct delegation)
- avoid unrelated cleanup or scope expansion
- not merge, rebase, or clean up reviewer-owned git state

Fix work is sequential. Do not parallelize it.

### Pre-Commit Checklist

Include the appropriate checklist verbatim in every delegated task that commits. The sub-agent must run all checks before `git commit`.

**Isolated delegation mode:**

1. `git branch --show-current` — must equal the temporary branch name given in the task. If it shows the feature branch, STOP and report without committing.
2. `git rev-parse --show-toplevel` — must equal the worktree path given in the task. If it shows a different path, STOP and report without committing.
3. `git status` — must show only files within the declared scope as modified. If unexpected files appear, STOP and report.

**Direct delegation mode:**

1. `git branch --show-current` — must equal the feature branch name given in the task. If it shows a different branch, STOP and report without committing.
2. `git status` — must show only files within the declared scope as modified. If unexpected files appear, STOP and report.

If any check fails, the sub-agent must not commit. It must report the mismatch and let the skill coordinator recover.

## Advisor Sanity Check

Called twice:

1. After initial analysis (step 6 in Analysis Flow) — internal quality gate before user sees findings
2. After the fix/review loop completes — before marking final status

Skip only if advisor budget is exhausted or `AdvisorEnabled` is off. Include the advisor's note in the final status summary.

## Steiner Delegation

Use Steiner's specialised tools directly:

- `explore({"task": "..."})` for analysis sub-agents
- `code({"task": "..."})` for approved fix passes
- `verify({"task": "..."})` for check-only verification

Specialised tools accept only `task`. Provide tight, self-contained task descriptions with context, files, constraints, expected output, non-goals, and the pre-commit checklist from the Fix/Review Loop section.

## Completion

Report final status (`pass`/`pass_with_notes`/`fail`) and summary of changes made. No closeout actions (no planning-doc cleanup or PR creation — that is the review skill's domain).
