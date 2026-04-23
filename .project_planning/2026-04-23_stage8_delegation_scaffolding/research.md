# Stage 8 Delegation Scaffolding — Research

## Question

1. Claude Code's `Agent` tool — exact name(s), parameter schema, isolation, result delivery, cancellation, parallel invocations.
2. OpenAI Codex / Agents SDK — sub-agent spawning exposure, schema, result shape.
3. Other frameworks (smolagents, LangGraph, AutoGen, Swarm, Cline) — brief comparison of delegation contracts.
4. Common shape of the result envelope returned to parent — standard fields, omissions.
5. Re-promptable child sessions vs one-shot delegation — prevalence and rationale.
6. Event/log shape for delegation lifecycle.
7. Risks and gotchas.

---

## Findings

### 1. Claude Code — `Agent` tool

**Tool name:** `Agent` (formerly `Task`; `Task(...)` still accepted as alias as of v2.1.63). Listed in tools-reference with `Permission Required: No`.

**How the model invokes it:** The model writes a natural-language task for the subagent. Claude Code internally constructs the subagent prompt; the model does not author the raw system prompt — it uses `@agent-<name>` mentions or relies on automatic delegation heuristics.

**Definition schema (frontmatter / `--agents` JSON):**

| Field | Type | Notes |
|---|---|---|
| `name` | string | Unique identifier, lowercase + hyphens |
| `description` | string | When Claude should delegate here |
| `prompt` | string | System prompt body (replaces default CC system prompt entirely) |
| `tools` | string list | Allowlist; inherits all if omitted |
| `disallowedTools` | string list | Denylist applied before `tools` |
| `model` | string | `sonnet`, `opus`, `haiku`, full model ID, or `inherit` |
| `permissionMode` | string | `default`, `acceptEdits`, `auto`, `dontAsk`, `bypassPermissions`, `plan` |
| `maxTurns` | int | Hard cap on agentic turns |
| `skills` | list | Injected at startup; NOT inherited from parent |
| `mcpServers` | list | Scoped MCP servers |
| `hooks` | map | Lifecycle hooks scoped to this subagent |
| `memory` | string | `user`, `project`, or `local` |
| `background` | bool | Run without blocking parent |
| `isolation` | string | Context isolation level |
| `effort` | string | Thoroughness hint |
| `color` | string | Display colour in TUI |

**Isolation:** Subagents start with a fresh context window. `CLAUDE.md` and project memory load normally through message flow, but parent conversation history is NOT passed. The subagent's system prompt replaces the default CC system prompt.

**Result delivery:** The subagent's final answer is returned as a tool result to the parent's agentic loop. The parent sees a summary/text output — not the full transcript. There is no structured fields envelope; it is the model's final text output.

**Cancellation:** Not explicitly documented as a programmatic API. Background subagents can be referenced by name in the typeahead and their status is shown. No explicit kill command exposed to the model; user can interrupt at TUI level.

**Parallel invocations:** Multiple `Agent` tool calls can be issued concurrently. Background subagents (`background: true`) run without blocking the parent turn. Claude Code caps concurrency via `provider.parallelism` settings (same scheduler used by steiner).

**Nesting:** Subagents can spawn further subagents via the `Agent` tool unless restricted with `Agent(agent_type)` syntax in the `tools` allowlist. Steiner has already decided to block nesting.

---

### 2. OpenAI Agents SDK

**Two delegation mechanisms:**

**A. Agent as tool** — an agent is wrapped as a callable tool. The parent LLM calls it like any function tool. The wrapped agent runs to completion and returns `result.final_output` (plain text or structured type) as the tool result. The caller never sees the child's internal turns. Schema: the tool description comes from the child agent's `name` and `handoff_description`; input schema is defined by `input_type` (a Pydantic model if needed).

**B. Handoff** — exposed as a tool named `transfer_to_<agent_name>`. Control transfers entirely to the new agent; it optionally receives filtered history via `input_filter`. Fields available in `HandoffInputData`: `input_history`, `pre_handoff_items`, `new_items`, `input_items` (override), `run_context`. Handoffs are permanent for that run; the original agent does not resume.

**Result envelope (RunResult):** `result.final_output` (string or typed), plus tracing metadata. No explicit "touched files" or "status code" fields — just the model's terminal output.

**Nesting:** Nested handoffs are opt-in beta (`RunConfig.nest_handoff_history`), disabled by default.

---

### 3. Other frameworks — brief comparison

| Framework | Delegation model | Tool exposed to model? | Result shape | Nesting |
|---|---|---|---|---|
| **smolagents** | `managed_agents` list on `MultiStepAgent`; child agents become callable tools with name+description. `provide_run_summary=True` makes child emit a summary instead of full output. | Yes — auto-registered as tool | Final answer string (or summary if `provide_run_summary`) | Yes, controlled by manager |
| **LangGraph** | Graph nodes; edges represent transitions. No single "spawn" tool — orchestration is code-level, not model-facing. | No — routing is graph edges | Node output dict, keyed by node | Subgraph nesting supported |
| **AutoGen** | `GroupChat` with a `GroupChatManager`; agents message each other. Delegation is conversational (model decides who to address). | Indirect — via message routing | Message string | Yes |
| **OpenAI Swarm** (predecessor) | Handoffs as function tools; `transfer_to_X()` pattern. Stateless per turn. | Yes | Agent's final message | No explicit nesting |
| **Cline / Aider** | Single-agent; no sub-agent spawning exposed to the model in standard flow. | No | N/A | N/A |

**Key convergence:** Almost universally, when delegation IS exposed to the model, it is as a function/tool call. The child runs to completion and returns a string (possibly structured). The parent does not see child's internal reasoning.

---

### 4. Result envelope — common fields

No universal standard, but the following fields appear consistently:

| Field | Present in | Notes |
|---|---|---|
| `output` / `final_output` | All | The child's terminal text or typed answer |
| `status` | Implicit / some | `success`, `error`, `timeout`, `cancelled` |
| `error` | OpenAI SDK, steiner-to-define | Error message if non-success |
| `agent_id` / `session_id` | Claude Code hooks | Opaque identifier |
| `agent_type` | Claude Code hooks | Name of subagent definition used |
| `turns_used` | Some | Useful for cost accounting |
| `touched_files` | NOT standard | No framework returns this; must be added by steiner explicitly if wanted |
| `summary` | smolagents optional | Separate short summary vs raw output |

**What gets omitted:** Full child transcript, tool call details, intermediate reasoning. These stay in the child's isolated context and are discarded at completion. This is intentional to prevent context bloat and transcript leakage.

---

### 5. Re-promptable vs one-shot delegation

**One-shot is by far the dominant pattern.** Claude Code's `Agent` tool, OpenAI agent-as-tool, smolagents managed_agents — all treat delegation as one-shot: parent issues task, child runs to completion, result returned.

**Re-promptable sessions** exist only in Claude Code's **agent teams** feature (persistent tmux-backed teammate sessions that can be messaged via `SendMessage`). This is the exception, not the rule.

**Why one-shot dominates:**
- Simpler state management (no session lifecycle to track)
- Natural fit for tool-call semantics (call → result)
- Prevents runaway sessions
- Easier cost accounting

**Steiner decision point:** The user spec says "parent may re-prompt the same child session." This is the harder pattern. Only agent-teams (CC) and AutoGen implement it. It requires session IDs, state persistence across calls, and a more complex child lifecycle.

---

### 6. Event/log shape for delegation lifecycle

No cross-vendor standard. Claude Code is the most explicit:

**Claude Code hook events:**
- `SubagentStart` — fires when `Agent` tool spawns a child
  - Input: `{ session_id, hook_event_name: "SubagentStart", agent_id, agent_type }`
  - Can inject `additionalContext` into child
- `SubagentStop` — fires when child finishes (converted from `Stop` in frontmatter hooks)
  - Input: `{ session_id, agent_id, agent_type }`
- `TaskCreated` — agent-teams only; teammate creates a task
  - Input: `{ task_id, task_subject, task_description, teammate_name, team_name }`
- `TaskCompleted` — agent-teams only; task finishes
  - Similar fields; supports `{"continue": false}` JSON control

**OpenAI Agents SDK:** Built-in tracing (spans) for each agent run, tool call, handoff. No standard event type names — vendor-specific.

**smolagents:** `step_callbacks` list; no named event types.

**Recommendation for steiner:** Define explicit event types mirroring CC's pattern: `DelegationStarted`, `DelegationCompleted`, `DelegationFailed`, `DelegationCancelled`.

---

### 7. Risks and gotchas

1. **Prompt injection via child output:** Child's output is returned verbatim as a tool result into the parent's context. A malicious or confused child could include instructions that alter parent behaviour. Mitigation: treat child output as data, not instructions; consider sanitising before injecting.

2. **Runaway children:** Without `maxTurns`, a child can loop indefinitely consuming tokens/cost. Always set a hard `maxTurns` default in config; tool args can only tighten it.

3. **Context size of returned results:** A verbose child that dumps a 50k-token summary causes parent context bloat. Mitigation: enforce an output size limit (e.g. 8k tokens) on the result envelope; truncate with a note.

4. **Cost surprises:** Each sub-agent is a full model session. Parallel sub-agents multiply cost linearly. Steiner must expose per-delegation cost metadata (tokens_in, tokens_out, turns_used) so the operator can track spend.

5. **Re-prompt session state:** If parent re-prompts a child, the child's conversation history grows. Without compaction, this eventually hits context limits. Need a compaction strategy for long-lived child sessions.

6. **No nesting enforcement at model level:** Without explicit tool-list restriction (`disallowedTools: Agent`), a child can try to spawn grandchildren. Steiner must enforce this at executor level, not just by policy.

7. **Result leakage into parent context:** Even a "summary" result can contain sensitive paths, keys, or intermediate reasoning. Consider a parent-visible vs parent-hidden split in result metadata.

---

## Implications

**`contract.go`:**
- Define `DelegationSpec` struct: `task_description string`, `system_prompt string` (optional override), `tools_allow []string`, `tools_deny []string`, `model string`, `max_turns int`, `timeout duration`, `output_limit_tokens int`, `background bool`
- `DelegationResult` struct: `output string`, `status string` (success/error/timeout/cancelled), `error string`, `agent_id string`, `turns_used int`, `tokens_in int`, `tokens_out int`
- No `touched_files` unless steiner explicitly tracks file mutations in the child executor

**`task.go`:**
- One-shot model is baseline. Re-promptable sessions need a `SessionHandle` with `agent_id` + channel for follow-up messages.
- Implement `SpawnDelegate(ctx, spec) (DelegationResult, error)` for one-shot
- Implement `OpenDelegate(ctx, spec) (SessionHandle, error)` + `SendDelegate(ctx, handle, msg) (DelegationResult, error)` for re-promptable

**`result.go`:**
- Enforce `output_limit_tokens` before returning to parent
- Include `status` enum: `success | error | timeout | cancelled | max_turns_reached`
- Strip raw internal transcript; only expose final model output

**`scaffold.go`:**
- Fresh context per child: new system prompt, no parent transcript
- Load project `AGENTS.md` through normal message flow (not context injection)
- Block `agent_spawn` tool in child executor unconditionally (enforce no-nesting at this layer)

**`limits.go`:**
- Default `max_turns: 20` (configurable global)
- Default `output_limit_tokens: 4096`
- Default `timeout: 5m`
- Tool args can only lower these values, never raise above config defaults

**Tool schema (model-facing):**
```json
{
  "name": "delegate",
  "description": "Spawn an isolated sub-agent to handle a self-contained task. Returns the sub-agent's final answer.",
  "parameters": {
    "task": { "type": "string", "description": "Full task description for the sub-agent" },
    "context": { "type": "string", "description": "Any context the sub-agent needs (no parent transcript is passed)" },
    "system_prompt": { "type": "string", "description": "Optional: override the sub-agent system prompt" },
    "model": { "type": "string", "description": "Optional: model override (cannot exceed parent's configured model tier)" },
    "max_turns": { "type": "integer", "description": "Optional: turn limit (cannot exceed config default)" },
    "background": { "type": "boolean", "description": "If true, return immediately with a handle; do not block" }
  },
  "required": ["task"]
}
```

**Event types (internal):**
- `DelegationStarted { agent_id, task_preview string, background bool }`
- `DelegationCompleted { agent_id, status, turns_used, tokens_in, tokens_out }`
- `DelegationFailed { agent_id, error }`
- `DelegationCancelled { agent_id }`

---

## Risks and Uncertainties

- **Re-promptable sessions add significant complexity.** One-shot covers 95% of use cases; re-promptable may be premature for Stage 8. Recommend shipping one-shot first.
- **Output size enforcement** requires token-counting the child result before returning it; steiner needs a tokeniser or character-count proxy.
- **Parallelism cap** via `provider.parallelism` is already in the architecture, but the child executor must acquire a slot before starting — confirm the scheduler contract allows this.
- **Model override policy:** The user spec says "tool args may tighten but not loosen" resource limits. Does this apply to model selection too (e.g., can a sub-agent use a more expensive model)? Needs clarification.

---

## Sources

- https://docs.anthropic.com/en/docs/claude-code/sub-agents
- https://docs.anthropic.com/en/docs/claude-code/agent-teams
- https://docs.anthropic.com/en/docs/claude-code/tools-reference
- https://docs.anthropic.com/en/docs/claude-code/hooks
- https://openai.github.io/openai-agents-python/
- https://openai.github.io/openai-agents-python/handoffs/
- https://huggingface.co/docs/smolagents/en/reference/agents

---

## Open Questions

1. **Re-promptable sessions in Stage 8?** One-shot is far simpler and covers most cases. Should re-promptable be deferred to Stage 9?
2. **Model override tightening:** Should sub-agents be restricted to the same model tier or lower? Or is any configured model fair game?
3. **touched_files tracking:** Do we want the executor to record which files a child mutated and surface that in `DelegationResult`? Useful for audit but adds complexity.
4. **Output truncation strategy:** Hard truncate at `output_limit_tokens`, or ask the child model to produce a summary if output exceeds the limit?
5. **Background delegation UX:** When `background: true`, how does the parent poll or receive the result? Push event via the existing output stream, or explicit `delegate_status(agent_id)` tool call?
6. **Nesting error behaviour:** If a child calls `delegate`, should the executor return a tool error (clean) or kill the child session (strict)? Clean error is safer.
