# Conversation Steering

## What is conversation steering?

Conversation steering lets the user type and send a prompt during an active model generation or tool execution, and have it influence the model's behavior on the next available turn — without hard-stopping the current operation. It is distinct from a destructive interrupt (Esc / Ctrl+C), which cancels the current work and discards context.

---

## Current state in steiner

Steiner has **no steering mechanism**. The only mid-run user interaction is a destructive interrupt:

- `Esc` / `Ctrl+C` / `Ctrl+D` in the TUI triggers `InterruptActiveRun`
- `InterruptActiveRun` → `ActiveRunController.Interrupt()` → `context.CancelFunc`
- The run terminates with `StopReasonCancelled`
- During an active conversation, the TUI **blocks all text input** — the user cannot type anything

There is no injection channel, queue, or steer action anywhere in the codebase.

### Key files (current interrupt flow)

| File | Role |
|------|------|
| `internal/interactive/actions.go` | Defines `InterruptActiveRun` (no `SteerPrompt`) |
| `internal/interactive/state.go` | `ActiveRunController` — cancel-only, no injection channel |
| `internal/interactive/run_flow.go` | `runWithInterruptOwnership` — wraps run in cancellable ctx only |
| `internal/interactive/session.go` | `Handle()` dispatches `InterruptActiveRun` — no steer case |
| `internal/agent/model_call.go:83-100` | `consumeModelStream` — pure drain loop, no injection select |
| `internal/provider/interface.go` | `Provider` interface — no steer method |
| `internal/tui/model_update_keys.go:95-98` | Esc/CtrlC triggers interrupt; all other keys blocked during active run |
| `internal/agent/runner.go` | Synchronous turn loop, no interstitial injection |

---

## How Claude Code does it

Claude Code documents two distinct mechanisms under the heading **"Interrupt and steer"**:

### Mechanism A: Esc / Ctrl+C — destructive interrupt

- Press `Esc` or `Ctrl+C` to stop Claude immediately.
- The running tool call is canceled, and Claude waits for the next instruction.
- File checkpoints are created before every edit. Pressing `Esc` twice rewinds to a previous checkpoint state.
- Known issues: Esc can remove the user's submitted prompt from chat history (GH #53674), and on Windows can permanently block text input (GH #27214).

### Mechanism B: Type + Enter — non-destructive queued steering

- **Type a correction and press `Enter`** while Claude is running.
- The message is **queued** — it does not interrupt the current operation.
- Claude reads the queued message **as soon as the current action completes** and adjusts before deciding its next step.
- The message appears in the UI as a "queued message."

### Important clarification: docs-vs-behavior

There is a known bug (GH #36326) documenting that **Enter does not actually interrupt mid-task** despite older docs implying it would. The message is only queued. To actually get the queued message read immediately, you must press `Ctrl+C` first (to stop the current tool), at which point Claude reads the queued input. The docs have since been updated to clarify: Esc interrupts, Enter queues.

### Delivery point: interstitial window

Claude Code runs a turn loop:

```
model generates response (possibly with tool calls)
  → Claude Code executes the tool
  → tool results feed back into context
  → model decides next step
  → repeat
```

Queued messages are injected **between tool calls** — at the "interstitial window" between steps. Feature request GH #30492 describes this explicitly:

> *"Between tool calls (Read, Edit, Bash, etc.), Claude Code already runs PreToolUse hooks. A native priority message system could use this same interstitial window to deliver queued user messages."*

### Additional steering pathways

- **System reminders**: Claude Code injects ~37 hidden `<system-reminder>` tags into the model's context reactively (post-compaction, file truncated, token warnings, etc.). These are silent nudges the user never sees.
- **Claude Agent SDK**: Lacks a real-time steering API. A closed feature request (GH #70) asked for a `session.send()` method for parity with Claude Code itself.
- **Prompt Queue + Steer Controls**: A feature request (GH #25845, closed as not planned) asked for a full queue UI with reorder/edit/remove capabilities.

---

## How OpenAI Codex CLI does it

### Primary mechanism: typing during active generation = steering

From the [Prompting docs](https://developers.openai.com/codex/prompting):

> *"You can continue steering Codex after the goal starts. Send follow-up messages to adjust constraints, such as asking Codex to use a particular library or avoid a specific approach."*

### The `turn/steer` RPC

Codex uses a formal app-server protocol with two endpoints:

| RPC | Behavior |
|-----|----------|
| `turn/steer` | Append user input to the active in-flight turn for a thread. Returns the accepted `turnId`. Does **not** emit `turn/started`. Does **not** accept turn overrides (model, sandbox, etc.). Purely appends user input to the in-flight turn. |
| `turn/interrupt` | Request cancellation of an in-flight turn. Success returns `{}`. The turn ends with `status: 'interrupted'`. |

The `turn/steer` API was added via PR #10821 (Feb 2026) to replace the earlier hack of calling `turn/start` while a turn was already running.

### Interrupt mechanism: Esc Esc (double-press)

- `Esc Esc` cancels the current in-progress action.
- Single `Esc` is easy to hit accidentally, so the double-press pattern was adopted (GH #14509).
- If the prompt is non-empty, `Esc Esc` clears the whole prompt instead.

### Slash commands during run: Tab queuing

From the [Slash Commands docs](https://developers.openai.com/codex/cli/slash-commands):

> *"When a task is already running, you can type a slash command and press `Tab` to queue it for the next turn. Codex parses queued slash commands when they run, so command menus and errors appear after the current turn finishes."*

Slash commands like `/model`, `/permissions`, `/fast`, `/personality`, `/plan`, `/goal` let you adjust session parameters mid-run.

### Side conversations

Codex supports `/side` conversations — ephemeral forks that let you interact with Codex without interrupting the main task. There is a known issue (GH #22599) where `Esc` can dismiss the side chat instead of submitting a queued steering prompt.

### Configurable no-interrupt mode

Issue GH #14693 (closed) requested a mode where **typing alone never interrupts the current turn** — reflecting that some users find accidental steering too easy. The TUI currently treats any typing as steering input.

### Experimental "steer conversation" feature

Issue GH #9524 reveals an experimental **"steer conversation"** feature flag. When enabled during context compaction, entering a prompt causes it to be consumed as a steering prompt for the compaction process itself.

---

## Cross-system comparison

| Aspect | Claude Code | OpenAI Codex CLI | steiner (current) |
|--------|-------------|------------------|-------------------|
| **Non-destructive steering** | Type + Enter → queued, delivered between tool calls | Typing during run → injected into active turn via `turn/steer` RPC | ❌ None |
| **Destructive interrupt** | `Esc` or `Ctrl+C` | `Esc Esc` or `/interrupt` | `Esc` / `Ctrl+C` / `Ctrl+D` (cancel context) |
| **Queue model** | Simple queue (one pending message at a time) | Full prompt queue via `Tab` for slash commands | ❌ None |
| **Delivery point** | Between tool calls (interstitial) | Appended to active turn context | N/A |
| **Public API for steering** | ❌ (not in Agent SDK) | ✅ `turn/steer` in app-server | N/A |
| **Mid-generation steering** | ❌ (waits for tool boundary) | ❌ (appended to context; model sees on next chunk) | N/A |
| **Undo/checkpoints** | File-level checkpoints on every edit; Esc×2 rewinds | Rollback via thread/rollback | ❌ None |
| **Slash commands during run** | N/A | `Tab` queues commands for next turn | N/A |
| **Side conversations** | ❌ | `/side` for ephemeral forks | ❌ |
| **Hidden steering** | ~37 system-reminder tags injected reactively | Not documented publicly | ❌ |

---

## Core insight

Neither Claude nor Codex truly interrupts mid-token-generation to steer. Both work at **boundaries**:

- **Claude**: tool call boundaries (the interstitial window between tool executions)
- **Codex**: turn-context boundaries (the steering text is appended to the active turn and the model sees it on the next chunk/turn iteration)

Steering is a **queue + injection-at-boundary** problem, not a stream-interruption problem.

---

## What steiner would need to implement steering

### 1. New action type

Add `SteerPrompt` to `internal/interactive/actions.go`:

```go
type SteerPrompt struct{ Text string }
```

### 2. Injection channel in ActiveRunController

Extend `internal/interactive/state.go` beyond just `context.CancelFunc` — add a `chan string` for steering messages, or a separate `SteerController` that the agent loop can select on.

### 3. Select on injection channel at interstitial points

In `internal/agent/runner.go`'s turn loop, add a `select` between the tool outcome and the steering channel. This mirrors Claude Code's interstitial-window approach.

Alternatively (Codex-style), inject the steering text directly into the conversation history/messages list so the model sees it on the next API call.

### 4. TUI input handling

In `internal/tui/model_update_keys.go`, stop blocking text input during active conversations. Instead:
- Capture text input
- Route it as `SteerPrompt` via the interactive session
- Show a "queued" indicator in the UI

### 5. Delivery semantics decision

Choose one of:

| Approach | Behavior | Complexity |
|----------|----------|------------|
| **Claude-style (queue + interstitial)** | Message is queued; the agent loop selects on the queue between tool calls. Current tool finishes first. | Medium — needs interstitial injection points in the agent loop |
| **Codex-style (append to turn context)** | Message is appended to the conversation messages immediately. Model sees it on the next API call. | Lower — simpler to implement, but model may act on steering mid-tool-execution |
| **Hybrid** | Queue the message, but allow Esc to flush it immediately (like Claude's Ctrl+C+Enter pattern) | Higher — both queue and interrupt coordination |

### 6. Provider considerations

The `internal/provider/interface.go` may or may not need changes:
- For Claude-style steering (injection at interstitial): no provider change needed — steering is a harness-level concern.
- For true mid-generation steering (injecting into a streaming response): would require a provider-side mechanism, which no major provider currently supports.

---

## Terminology

| Term | Origin | Meaning |
|------|--------|---------|
| **"Interrupt and steer"** | Claude Code docs | The Esc/Enter dual mechanism |
| **"Conversation steering"** / **"steer conversation"** | Codex CLI experimental feature flag | General term for mid-run prompt injection |
| **"Mid-turn input"** | Codex app-server protocol (`turn/steer`) | User input appended to an in-flight turn |
| **"Streaming interrupt"** | General | Aborting a streaming model response |
| **"Priority message channel"** | Claude Code GH #30492 (proposed) | A side-channel for high-priority user messages |
| **"Queued message"** | Claude Code | A message typed during active generation, delivered later |
| **"System reminders"** | Claude Code | Hidden `<system-reminder>` tags injected reactively into context |
| **"Real-time steering"** | Claude Agent SDK GH #70 | Steering during active generation (not yet implemented) |

---

## Key sources

| Source | URL |
|--------|-----|
| Claude Code docs: How Claude Code Works | https://code.claude.com/docs/en/how-claude-code-works |
| Claude Code GH #30492: Real-time steering | https://github.com/anthropics/claude-code/issues/30492 |
| Claude Code GH #25845: Prompt Queue | https://github.com/anthropics/claude-code/issues/25845 |
| Claude Code GH #36326: Enter queues, not interrupts | https://github.com/anthropics/claude-code/issues/36326 |
| Claude Agent SDK GH #70: Real-Time Steering | https://github.com/anthropics/claude-agent-sdk-typescript/issues/70 |
| System reminders blog post | https://michaellivs.com/blog/system-reminders-steering-agents/ |
| BSWEN: How to Stop Claude Code | https://docs.bswen.com/blog/2026-03-25-how-to-stop-claude-code-mid-execution/ |
| Codex Slash Commands docs | https://developers.openai.com/codex/cli/slash-commands |
| Codex Prompting docs | https://developers.openai.com/codex/prompting |
| Codex App Server docs (turn/steer, turn/interrupt) | https://developers.openai.com/codex/app-server |
| Codex PR #10821: turn/steer API | https://github.com/openai/codex/pull/10821 |
| Codex GH #14693: no-interrupt mode | https://github.com/openai/codex/issues/14693 |
| Codex GH #12329: Expose turn/steer in SDK | https://github.com/openai/codex/issues/12329 |
| Codex GH #14509: Esc Esc pattern | https://github.com/openai/codex/issues/14509 |
| Codex GH #9524: steer conversation experimental | https://github.com/openai/codex/issues/9524 |
| Codex GH #22599: side chat Esc bug | https://github.com/openai/codex/issues/22599 |
