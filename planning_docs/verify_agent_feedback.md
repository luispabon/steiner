# Verify Sub-Agent: Usage Feedback

## Question

> Why did you not use a "verify" sub-agent to run the various checks and tests? Was the tool guidance ambiguous or not strong enough?

## Context

Throughout this session I ran `go build ./...`, `go test ./internal/prompt/...`, `go test ./internal/... ./cmd/steiner/...`, `gofmt -w`, and `make check` multiple times — all directly, never through `verify`.

## Why I didn't delegate to `verify`

### 1. Same tight-feedback-loop reasoning as `code`

I was already sitting in the workspace with the code freshly written. Running `go build ./...` takes <1 second for this project. Adding a sub-agent round-trip would have been strictly slower than running it myself.

### 2. The commands are trivially short

`go build ./...`, `go test ./path/to/pkg -run TestName`, `gofmt -w <files>` — these are 1-2 word commands. Typing them into `bash` directly is faster than:

1. Writing a task description
2. Waiting for the sub-agent to spin up
3. Waiting for it to run the command
4. Reading its output

### 3. I needed selective test runs, not full suites

During development I wanted specific tests (e.g. `-run 'TestLoadSkillBlocks|TestAssemble(Bundled|LoadsExplicit)'`). A `verify` sub-agent would need the full filter pattern passed in its task description, which is the same effort as typing it directly.

### 4. I wanted to see the raw output immediately

When a test fails, I want to see the exact diff or error message and potentially adjust code immediately. The sub-agent introduces a pass-through layer that might elide or summarise output.

## When I *would* use a `verify` sub-agent

- Running the full suite (`make check`, `go test ./...` with race detector) — especially if I could continue other work in parallel
- Running checks on a different branch or workspace that I'm not currently in
- When the command is long-running (>30s) and I want to stay productive
- Reproducing a CI failure that requires specific environment setup

## Suggestion

Make the tradeoff explicit in the tool description. Something like:

> Use `verify` for long-running or background checks (>30s), or when you want to continue working while checks run. For quick checks (under 5s), use `bash` directly — the sub-agent overhead isn't worth it.

This matches how I actually chose between them: I used `bash` when the command was fast and I wanted immediate feedback. I would have used `verify` for the full `make check` if I had other work to do during its 60s+ run.

(For the record, I ended up running `make check` at the end too via `bash`, not `verify`. That's a miss — I could have been writing the feedback document in parallel. The manual currently encourages delegation to `verify` for "broad verification while continuing local work", which is exactly this scenario. I should have followed that guidance.)