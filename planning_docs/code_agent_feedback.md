# Code Sub-Agent: Usage Feedback

## Question

> Why did you not use a "code" sub-agent to do the implementation? Was the tool guidance ambiguous or not strong enough?

## Context

The implementation involved coordinated edits across 5 files in 2 packages (`cmd/steiner/` and `internal/prompt/`) to thread a single value (`SkillsBundledFS`) from a new field on `cliRuntime` through `AssemblyOptions` into `skill.Loader.BundledFS`.

## Why I didn't delegate to `code`

### 1. I was already mid-stream, with full context loaded

By the time I started implementing, I had:
- Read all 5 target files directly (they were in my local working set)
- Traced the data flow end-to-end in my head
- Already identified every insertion point and the exact content needed

A sub-agent would have had to re-discover all of this from scratch. The cost of re-establishing context across the handoff exceeded the cost of just writing the changes directly.

### 2. The change was structurally simple but whitespace-sensitive

The edits were mostly "add one field" or "add one argument". But each one required precise whitespace matching (tab-indented Go struct fields). The `mutate` tool's `replace` operation needs exact `old_string` matches. I found it faster to just read the line, craft the string, and apply it myself than to:

1. Write a task description precise enough that the sub-agent would get every indentation right
2. Wait for the sub-agent to run
3. Review the result and fix any formatting issues

### 3. Tight feedback loop with `go build`

After each change I could immediately run `go build ./...` or `gofmt -w <files>` to verify. A sub-agent would have added latency to this loop.

### 4. Risk of the sub-agent making broader changes

A `code` sub-agent receives a task description and has autonomy to implement it. For a tightly scoped bug fix with known exact edits, I preferred to control every character rather than describe the intent and review the output.

## When I *would* use a `code` sub-agent

- The change is self-contained in one package or one file
- The success criteria are well-defined (e.g. "add a function that does X, with tests that pass")
- I don't already have the exact code in my working context
- The risk of the sub-agent making unexpected design decisions is low

## Suggestion

If the tool guidance wanted to push more work to `code` sub-agents, it could be clearer about the threshold. Currently the manual says to delegate to `code` for "a scoped change" but also says to work locally when "a single tool call completes the task" or "you are already mid-edit in the same file". I was in the latter category — already mid-edit — so the manual's own logic pointed me toward working locally.

What would help is a clearer heuristic: **if you can describe the change in 2-3 sentences and don't need to read the files first, delegate to `code`. If you're already holding the exact code in your working context, finish it locally.**