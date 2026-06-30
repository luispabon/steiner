# Sub-agent delegation

`steiner` exposes six sub-agent-as-tool operations that delegate bounded tasks to isolated child agents. This document covers both user-facing usage (tools, configuration, safety) and the internal delegation machinery.

`advisor` is separate from delegation: it is a stronger-model steering pass over the live parent conversation, with no tools and no child loop. The advisor lives alongside the delegation tools in the main loop, but it is not a child agent.

---

## Part 1 — User guide

### Available tools

Sub-agent delegation is **enabled by default**. When it is, the model sees six additional tools alongside the built-in ones:

| Tool        | What it does                                                                     | Extra params                                               | Can mutate?            |
|-------------|----------------------------------------------------------------------------------|------------------------------------------------------------|------------------------|
| `explore`   | Navigate the codebase to find files, symbols, call sites, and patterns           | `task` only                                                | No                     |
| `research`  | Gather and synthesise information from the codebase or web                       | `task` only                                                | No                     |
| `code`      | Implement a scoped change — read relevant files, write changes, run tests        | `task` only                                                | Yes (`mutate`, `bash`) |
| `plan`      | Analyse a sub-problem, evaluate options, and produce a structured recommendation | `task` only                                                | No                     |
| `verify`    | Run tests, linters, builds, or other checks and report pass or fail              | `task` only                                                | No                     |
| `follow_up` | Resume an existing sub-agent session by agent ID with a new user message         | `agent_id`, `message`                                      | No (resumes existing)  |

The five specialised tools (`explore`, `research`, `code`, `plan`, `verify`) are hardcoded with purpose-built system prompts and tool allowlists. The `follow_up` tool resumes a previously delegated child agent while preserving its conversation history. The parent-only `workflow_handoff` tool creates a handoff request for the current session; it is not exposed to child agents yet.

### Advisor

The `advisor` tool is a pure reasoning pass for the parent agent. It reads the live parent conversation, calls a stronger model, and returns concise strategic guidance. It does **not** expose any tools, does **not** start a child loop, and does **not** mutate state. Its per-run cap is enforced in handler state so the tool definition stays stable for prompt-cache integrity.

### When to use each

| Situation                                              | Tool                                                           |
|--------------------------------------------------------|----------------------------------------------------------------|
| Find DRY/refactoring opportunities across the codebase | `explore` — report files, repeated patterns, risks, next steps |
| Fix a bug but location is unknown                      | `explore` — search likely areas and report exact files/code    |
| Need to understand an external API or library          | `research` — gather docs, usage examples, and constraints      |
| Implement a small known change in one package          | `code` — implement if ownership and tests are clear            |
| Understand how a feature works across multiple files   | `explore` — trace the call chain and report                    |
| Evaluate two approaches to a design problem            | `plan` — analyse tradeoffs and recommend                       |
| Run broad verification while continuing local work     | `verify` — run checks and summarise exact failures             |

`plan` is for focused sub-problem analysis, **not** overall task planning.

### `follow_up`

The `follow_up` tool lets the parent model send a new user message to an existing child session identified by `agent_id`. This is useful when a sub-agent's initial response leads to follow-up questions or iterative refinement.

Key behaviours:

- **Preserves conversation** — the child's prior message history is retained and the new message is appended.
- **Resets budget** — each follow-up resets the child's turn and token budgets to the configured defaults (not the remaining budget from the prior run).
- **Tracks follow-ups** — the returned result includes a `follow_up_count` field so the parent can see how many follow-ups have occurred.
- **Auto-approved** — the `follow_up` tool is approval mode `auto` (no user gate).
- **No nesting** — `follow_up` is stripped from child agent registries, so sub-agents cannot follow-up on other sub-agents.

### Safety

- A sub-agent **cannot delegate further** — `delegate` and `follow_up` tools are always stripped from child registries.
- The parent-only `workflow_handoff` tool is not included in child allowlists yet.
- Only the `code` sub-agent has access to file-mutation tools (`mutate`) or `bash`.
- `explore`, `research`, and `plan` are read-only.
- `verify` can run commands via `bash` but must not modify files.
- All sub-agent tools are automatically approval-gated as `auto` — no manual prompt is needed to use them.
- The child's full conversation transcript is not copied into the parent session; only a structured result and bounded summary persist.

### Default tool allowlists

| Agent      | Tools available                                             |
|------------|-------------------------------------------------------------|
| `explore`  | `read`, `glob`, `grep`, `ls`                                |
| `research` | `read`, `glob`, `grep`, `ls`, `web_search`\*, `fetch_url`\* |
| `code`     | `read`, `glob`, `grep`, `ls`, `mutate`, `bash`              |
| `plan`     | `read`, `glob`, `grep`, `ls`                                |
| `verify`   | `read`, `glob`, `grep`, `ls`, `bash`                        |

\* `web_search` and `fetch_url` are not yet implemented. The `research` agent won't be fully available until a `web_search` backend is configured — see the README for details.

### Configuration

Sub-agents are configured under the `sub_agent` key in `config.yaml`:

```yaml
sub_agent:
  # Master switch — set to false to remove all sub-agent tools from the model.
  enabled: true

  # Default limits for all sub-agents (the code applies a floor of 15 turns).
  max_turns: 30
  max_tokens: 100000

  # Per-agent-type model overrides. When set, sub-agents of that type use
  # a different model than the parent agent.
  agents:
    code:
      model: gpt-4o
    research:
      model: claude-sonnet-4
```

Each entry under `agents` keyed by agent type name can set `model` to any model alias defined in your `models` configuration. If no override is set, the sub-agent uses the same model as the parent.

---

For architecture and implementation details, see [Sub-agent Delegation Internals](sub-agent-delegation-internals.md).
