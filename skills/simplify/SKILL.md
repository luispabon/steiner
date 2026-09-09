---
name: simplify
description: Analyze branch changes for code quality improvements across reuse, simplification, efficiency, and altitude categories using parallel sub-agents. Produces a report and fix plan, then runs an approved fix/review loop. Use on a feature branch to clean up before review.
---

# Code Simplification Analysis

## Overview

Use this skill on a feature branch to analyze changes for structural and quality improvements. The workflow analyzes changed files against four quality categories (Reuse, Simplification, Efficiency, Altitude) using parallel explore sub-agents, aggregates findings through an advisor review, and presents a report to the user. If fixes are approved, the skill enters a fix/review loop with optional verification reruns. This skill does NOT hunt for bugs — that is `/review`'s domain. All proposed changes must preserve existing behavior.

## Input Contract

- Accept an optional argument for the base branch (default: `main`).
- Require the current branch to differ from the selected base branch — error only if the current branch equals that base branch.
- No planning artifacts required — this skill operates directly on the branch diff.

## Analysis Flow

Follow this sequence:

1. Determine base branch (argument or `main`).
2. Compute changed files from every relevant state: committed changes from the merge-base to `HEAD` (`git diff --name-status --find-renames "$(git merge-base <base> HEAD)" HEAD`), staged changes (`git diff --cached --name-status --find-renames`), unstaged changes (`git diff --name-status --find-renames`), and untracked files (`git ls-files --others --exclude-standard`). Preserve status, rename, and delete information, including both paths for renames where available. Deduplicate paths without discarding status. Read existing files directly; inspect deleted paths through diffs.
3. If no changed files, report "nothing to analyze" and stop.
4. Present a summary of changes to the user: a title, and a brief high-level description
5. Dispatch all four category analyses together, using one parallel `explore` sub-agent per category. Runtime concurrency is bounded by configured `sub_agent.max_parallel`. Each receives: the list of changed files, the base branch name, and the category-specific analysis prompt from ## Category Prompts. Each agent must return findings as a structured list with: finding ID (category prefix + number, e.g. R1, S2, E3, A1), severity (blocking/non_blocking/informational), file path and line range, description, and suggested fix.
6. Aggregate all four reports into a unified findings list.
7. Call the advisor with the full aggregated findings in `question`, framing the requested review. Pass only real repository paths via `files` where useful. Incorporate feedback: drop findings the advisor flags as weak or incorrect, adjust severity per advisor guidance, add concerns the advisor raises that sub-agents missed.
8. Present the refined report to the user, organized by category (Reuse, Simplification, Efficiency, Altitude), with finding counts and severity breakdown.

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

Run the approved fix/review loop:

- Delegated fix pass (a `code` sub-agent in its own runtime-provisioned worktree) is preferred over inline fixes. Worktree provisioning failure is a blocker to report, not a cue for inline fixes. Use inline fixes only when delegation tools themselves are unavailable.
- Fix work is sequential, not parallel
- After fixes, run the narrowest scoped checks covering the fixes or `make check`. Before `make check` or `golangci-lint run`, run `golangci-lint cache clean`.
- Lightweight re-review: check whether fixes introduced new quality issues in the same four categories. Repeat only if new blocking findings emerge. Cap at 2 fix iterations.

### Warm Follow-Up Policy

Resume a suitable warm agent before cold dispatch only when it remains available for the same bounded deliverable in the same still-live workspace and scope. Follow-ups are sequential. Keep the responsible code agent and its worktree alive through warm `follow_up` calls for related corrections. Do not merge or clean up its branch or worktree until the correction loop completes; then merge the returned branch and remove the worktree and branch. A resumable session alone does not prove that an isolated worktree still exists. Use the responsible implementation agent for related corrections and the original reviewer only for a narrow re-check. Use fresh delegation for unavailable or non-resumable sessions, material lane or scope changes, independent or wider review, or removed worktrees. Workflow handoffs are not safe continuation boundaries.

### Worktree Handling

Every `code` sub-agent runs in its own runtime-provisioned and runtime-verified git worktree on a `delegate/` branch under `.steiner/worktrees/`; you arrange nothing yourself.

1. Read `worktree_path` from the delegation result — a project-relative path (e.g. `.steiner/worktrees/<process>/<branch>/<agent>`) and the sole worktree locator returned to you. The branch name and any dirty-tree warnings are host-only and not returned; if you need the branch name, read it from the worktree itself: `git -C <worktree-path> branch --show-current`.
2. `follow_up` results also carry `worktree_path` for the same code agent, resolving to the same worktree as the initial `code` result.
3. After reviewing a step's result, merge the returned branch into the feature branch first, then remove the worktree and delete the branch, in that order: `git worktree remove <worktree-path>`, then `git branch -D <branch-name>` (from step 1).

### Fix/Review Delegation

The fix delegated agent must:

- receive only the approved findings, fix plan, relevant files, constraints, and verification strategy
- run the pre-commit checklist before committing (see below)
- commit its changes on the runtime-provided `delegate/` branch
- avoid unrelated cleanup or scope expansion
- not merge, rebase, or clean up reviewer-owned git state

Fix work is sequential. Do not parallelize it.

### Pre-Commit Checklist

Include this checklist verbatim in every delegated task that commits. The sub-agent must run all checks before `git commit`.

1. `git branch --show-current` — must start with `delegate/`. If it shows the feature branch, STOP and report without committing.
2. `git status` — must show only files within the declared scope as modified. If unexpected files appear, STOP and report.

If any check fails, the sub-agent must not commit. It must report the mismatch and let the skill coordinator recover.

## Advisor Sanity Check

Called twice, each time putting the aggregated findings in `question` and passing only real repository paths via `files` where useful:

1. After initial analysis (step 7 in Analysis Flow) — internal quality gate before user sees findings
2. After the fix/review loop completes — before marking final status

Skip only if the advisor is disabled or unavailable. Include the advisor's note in the final status summary.

## Completion

Report final status (`pass`/`pass_with_notes`/`fail`) and summary of changes made. No closeout actions (no planning-doc cleanup or PR creation — that is the review skill's domain).
