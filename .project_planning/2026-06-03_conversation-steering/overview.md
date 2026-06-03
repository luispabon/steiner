## Request

Implement conversation steering: allow users to type and send a prompt while the model is generating a response. The message is queued and delivered on the next available turn boundary, letting the user steer without waiting for the current turn to finish. Ref: GitHub issue #92.

## Overview

Conversation steering adds a non-destructive input pathway alongside the existing destructive interrupt (Esc/Ctrl+C). Users type a message and press Enter during an active run. The message is queued (one pending message at a time). The agent loop checks the queue between turns and, if a steering message is present, injects it as a user message into the conversation before the next model call.

### Architecture

Three layers change:

1. **TUI layer** (`internal/tui`): Unblock text input during active conversations. Route Enter → `SteerPrompt` action instead of swallowing keystrokes. Show a "queued" visual indicator.

2. **Interactive layer** (`internal/interactive`): New `SteerPrompt` action type. Extend `ActiveRunController` with a steering channel (`chan string`, capacity 1). Session dispatches `SteerPrompt` by sending to the channel.

3. **Agent layer** (`internal/agent`): `RunRequest` gains an optional `SteerCh <-chan string`. The `Runner.Run` outer loop checks `SteerCh` between turns (non-blocking select). When a steering message arrives, it is appended as a user message to the conversation and the loop continues to the next turn.

### Delivery semantics

- **Queue model**: Single pending message. If the user sends a second steer before the first is consumed, the second replaces the first (latest-wins). This keeps the channel non-blocking and avoids unbounded queues.
- **Injection point**: Between turns only — after `p.advance()` returns and before the next `stopRunBeforeTurn` check. The current turn's tool calls all complete first.
- **Message format**: Injected as a standard `agent.Message{Role: MessageRoleUser}` so the model sees it as a normal user message in context.
- **Interaction with interrupt**: Esc/Ctrl+C still cancels the run. If a steer is queued when interrupt fires, the steer is discarded (the run is cancelled). The user can re-submit after the run stops.
- **Interaction with compaction**: No special handling. Steering messages are regular user messages and participate in compaction normally.

### What does NOT change

- Provider interface — steering is harness-level.
- Tool execution — tools run to completion within a turn.
- Approval flow — approval prompts continue to block; steering input during approval goes to the approval handler, not the steer queue.
- `--exec` mode — steering is interactive-only.

### Risks

1. **Race between steer send and run completion**: The run goroutine finishes and clears the controller while a steer is in-flight. Mitigated by: channel is buffered(1), send is non-blocking, and controller clear drains residual messages.
2. **TUI state confusion**: Input field serves dual purpose (prompt when idle, steer when active). Need clear visual distinction. Mitigated by: "queued" indicator and potentially different input styling.
3. **Steer during compaction**: A turn that triggers compaction retries the turn. The steer check happens in the outer loop, so compaction retries don't consume steers prematurely. No special handling needed.

## Verification Strategy

Commands discovered from repo:

| Command | Cost | Notes |
|---------|------|-------|
| `gofmt -w <files>` | Cheap | Format after edits |
| `goimports -w <files>` | Cheap | Fix imports |
| `go vet ./...` | Cheap | Static analysis |
| `go build ./...` | Medium | Full build |
| `go test ./path/to/pkg -run TestName` | Cheap | Targeted test |
| `go test ./...` | Medium | Full test suite |
| `go test -race ./...` | Expensive | Race detector |
| `golangci-lint run ./...` | Medium | Lint |
| `make check` | Expensive | Full verification (mandated by CLAUDE.md) |
| `make build-binaries` | Medium | Binary build |

Strategy:
- Run `gofmt -w` and `goimports -w` after each step's edits.
- Run targeted `go test` per step for changed packages.
- Run `go build ./...` after steps that change interfaces.
- Run `make check` after final step.
- Run `go test -race ./...` specifically for the concurrency-sensitive steering channel code.

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | Between-turns injection only | Simpler, matches Claude Code behavior, avoids mid-batch state issues. User-confirmed. |
| 2 | Single pending message (latest-wins) | Avoids unbounded queue complexity. Simple buffered channel. |
| 3 | No provider changes | Steering is harness-level orchestration, not model-transport. |
| 4 | Standard user message injection | Model sees steer as normal conversation. No special message role needed. |
