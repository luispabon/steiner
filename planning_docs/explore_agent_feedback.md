# Explore Sub-Agent: Feedback and Observations

## Context

I (the coding agent) was asked to investigate a bug where bundled skills (`plan`, `implement`, `review`) errored with `skill "%s" not found in any root`. I delegated the investigation to the `explore` sub-agent, then after it returned, I did additional reading of the same files myself before implementing the fix.

## What the sub-agent did well

- Traced both the discovery and loading code paths completely
- Identified the critical architectural disconnect: `BundledFS` was set during discovery (`discoverRuntimeSkills`) but **nil** during loading (`skillsStep`)
- Called out every relevant file with precise line numbers
- Named the root cause and the general direction of the fix
- Distinguished between the two overlapping concepts (bundled skills vs delegate agent tools) — this saved me from chasing a wrong lead
- Structured the output clearly with tables showing the disconnect

## Why I still did additional exploration afterward

### 1. Exact byte-level text needed for mutation

The `mutate` tool with `type: replace` requires an exact `old_string` match including whitespace. The sub-agent gave me line numbers and descriptions, but not the precise indentation and surrounding code I needed to craft working mutations. I had to read the raw source to get the exact strings.

*Example: The sub-agent said `source_plan.go:131` created a Loader without BundledFS. But to write the replace, I needed the full line including the 8-space tab prefix and the surrounding function context.*

### 2. Need to load the code in working memory for multi-file edits

When changing four files across two packages (`cmd/steiner/runtime.go`, `cmd/steiner/runtime_build.go`, `cmd/steiner/runner_run.go`, `internal/prompt/source_plan.go`, `internal/prompt/types.go`), I need the actual code in my local working context. The sub-agent's summary was good for orientation, but insufficient for coherent implementation across the stack.

### 3. Secondary call sites not covered by the original ask

The sub-agent focused on the specific question I gave it. When implementing, I discovered I also needed:
- How `AssemblyOptions` is populated in `runner_run.go::promptAssembly()`
- How `cliRuntime` is constructed in `runtime.go::defaultBuildRuntime()`
- The exact return signature of `discoverRuntimeSkills()` to know what to change

The sub-agent didn't include these because I didn't ask for them. It answered exactly what I asked — which is correct behaviour, but the handoff from analysis to implementation required more context.

## Suggestions for improvement

### 1. Include exact code snippets in analysis output

When reporting on a specific line, include the full line (with whitespace) from the source file, not just the line number and description. E.g. instead of:

> Line 131 creates `skill.Loader{RootDirs: skillRoots}` without BundledFS

Include:

> Line 131: `skillBlocks, err := loadSkillBlocks(ctx, skill.Loader{RootDirs: skillRoots}, opts.SkillNames)`

This would let me copy the exact string into a `mutate` `old_string` parameter without an additional read.

### 2. Surface all call sites by default

When asked about a function or struct, list **all** call sites in the report, not just the primary one. For example, when asked about `AssemblyOptions`, include:
- Where it's defined (`types.go`)
- Where it's populated (`runner_run.go:112-127`, `assemble_test.go` multiple locations)
- Where each field is consumed

This would reduce the need for follow-up reads.

### 3. Offer a diff or patch suggestion

After identifying the bug, the sub-agent could optionally produce a small pseudo-diff showing the minimal change needed. This would serve as a more actionable bridge between analysis and implementation.

### 4. Track what `read` calls I make afterward

The sub-agent could ask itself: "If the caller wanted to implement this fix, what exact code would they need to mutate?" and pre-include that in the output.

### 5. Categorise findings by actionability

Separate the output into:
- **Diagnosis** (what's happening)
- **Required code changes** (what needs to change, with exact current code)
- **Secondary context** (extra files the implementer will likely need)

This would let me skip directly to implementation when the diagnosis is clear.

## Summary

The sub-agent performed well on the analysis task. The gap is that **analysis is not implementation readiness** — I need exact source text in my local context to plan edits precisely. This isn't a failing of the agent; it's a handoff problem between "figure out what's wrong" and "fix it." The suggestions above aim to make the analysis output more directly actionable.
