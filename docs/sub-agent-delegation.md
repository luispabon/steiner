# Sub-agent delegation

`steiner` exposes eight sub-agent-as-tool operations that delegate bounded tasks to isolated child agents.

`advisor` is separate from delegation: it is a stronger-model steering pass over the live parent conversation, with no tools and no child loop. The advisor lives alongside the delegation tools in the main loop, but it is not a child agent.

---

## Available tools

Sub-agent delegation is **enabled by default**. When it is, the model sees eight additional tools alongside the built-in ones:

| Tool        | What it does                                                                     | Extra params                                               | Can mutate?            |
|-------------|----------------------------------------------------------------------------------|------------------------------------------------------------|------------------------|
| `explore`   | Navigate the codebase to find files, symbols, call sites, and patterns           | `task` only                                                | No                     |
| `research`  | Gather and synthesise information from the codebase or web                       | `task` only                                                | No                     |
| `code`      | Implement a scoped change — read relevant files, write changes, run tests        | `task` only                                                | Yes (`mutate`, `bash`) |
| `evaluate`    | Analyse a sub-problem, evaluate options, and produce a structured recommendation | `task` only                                                | No                     |
| `sanity_check`| Run tests, linters, builds, or other checks and report pass or fail              | `task` only                                                | No                     |
| `review`      | Examine code changes for bugs, regressions, missing tests, or plan adherence     | `task` only                                                | No                     |
| `vision`    | Analyze an image by ID — the sub-agent receives the image directly               | `task`, `image_id`                                         | No                     |
| `follow_up` | Resume an existing sub-agent session by agent ID with a new user message         | `agent_id`, `message`                                      | No (resumes existing)  |

The six specialised tools (`explore`, `research`, `code`, `evaluate`, `sanity_check`, `vision`) plus `review` are hardcoded with purpose-built system prompts and tool allowlists. The `follow_up` tool resumes a previously delegated child agent while preserving its conversation history. The parent-only `workflow_handoff` tool creates a handoff request for the current session; it is not exposed to child agents yet.

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
| Evaluate two approaches to a design problem            | `evaluate` — analyse tradeoffs and recommend                   |
| Run broad verification while continuing local work     | `sanity_check` — run checks and summarise exact failures       |
| Review implemented changes before merge                | `review` — examine code for bugs, regressions, missing tests   |
| Describe or query a pasted image                       | `vision` — the sub-agent receives the image and answers        |

`evaluate` is for focused sub-problem analysis, **not** overall task planning.

`review` is for examining implemented changes — it does not plan, implement, or verify.

### `follow_up`

The `follow_up` tool lets the parent model send a new user message to an existing child session identified by `agent_id`. This is useful when a sub-agent's initial response leads to follow-up questions or iterative refinement.

Key behaviours:

- **Preserves conversation** — the child's prior message history is retained and the new message is appended.
- **Resets budget** — each follow-up resets the child's turn and token budgets to the configured defaults (not the remaining budget from the prior run).
- **Tracks follow-ups** — the returned result includes a `follow_up_count` field so the parent can see how many follow-ups have occurred.
- **Auto-approved** — the `follow_up` tool is approval mode `auto` (no user gate).
- **No nesting** — `follow_up` is stripped from child agent registries, so sub-agents cannot follow-up on other sub-agents.

### Safety

- A sub-agent **cannot delegate further** — `follow_up` and `workflow_handoff` tools are always stripped from child registries.
- The parent-only `workflow_handoff` tool is not included in child allowlists yet.
- Only the `code` sub-agent has access to file-mutation tools (`mutate`) or `bash`.
- `explore`, `research`, `evaluate`, `review`, and `vision` are read-only.
- `sanity_check` can run commands via `bash` but must not modify files.
- All sub-agent tools are automatically approval-gated as `auto` — no manual prompt is needed to use them.
- The child's full conversation transcript is not copied into the parent session; only a structured result and bounded summary persist.
- While the parent interactive session is in `plan` execution mode, the `code` sub-agent tool is denied outright, and `follow_up` is denied when it targets a session spawned by `code` — both can mutate files, which plan mode disallows. See [docs/execution-modes.md](execution-modes.md) for the full enforcement matrix.

### Default tool allowlists

| Agent      | Tools available                                             |
|------------|-------------------------------------------------------------|
| `explore`  | `read`, `glob`, `grep`, `ls`                                |
| `research` | `read`, `glob`, `grep`, `ls`, `web_search`\*, `fetch_url`\* |
| `code`     | `read`, `glob`, `grep`, `ls`, `mutate`, `bash`              |
| `evaluate`    | `read`, `glob`, `grep`, `ls`                                |
| `sanity_check`| `read`, `glob`, `grep`, `ls`, `bash`                        |
| `vision`   | `read`                                                      |
| `review`      | `read`, `glob`, `grep`, `ls`, `bash`                        |

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

models:
  # Per-agent-type model overrides. When set, sub-agents of that type use
  # a different model than the parent agent.
  sub_agents:
    code: gpt-4o
    evaluate: claude-sonnet-4
    sanity_check: gpt-4o-mini
```

Each entry under `models.sub_agents` keyed by agent type name can set the model alias to any key defined in `models.definitions`. If no override is set, the sub-agent uses the same model as the parent.

### Recommended model tiers

Model tier recommendations help choose which model to assign to each agent type.
These are **recommendations, not enforcement** — users configure `models.sub_agents.<type>` freely.

| Tier          | Agent types                             | Rationale                                                                 |
|---------------|-----------------------------------------|---------------------------------------------------------------------------|
| **Flash**     | `explore`, `code`, `sanity_check`       | Mechanical tasks — search, implement scoped changes, run checks. Speed matters more than depth. A flash-tier model also works well for the initial `explore` that pre-digests the design during implementation. |
| **Balanced**  | `research`, `review`, `vision`          | Requires synthesis or judgment — gathering information, reviewing code changes, analyzing images. Needs a model that can weigh context and produce nuanced output. |
| **Top-thinker** | `evaluate`                           | Deep reasoning on scoped sub-problems — analysing tradeoffs, evaluating approaches. Benefits most from a stronger model's deliberation capacity. |

### `vision` tool

The `vision` tool requires two parameters:

| Parameter  | Type   | Description |
|------------|--------|-------------|
| `task`     | string | What to analyze or describe about the image. |
| `image_id` | string | The image ID shown in the placeholder (e.g. `img-1`). |

When you paste an image, the TUI displays its assigned ID below the submitted message. Pass that ID to `vision` to examine the image.

After the initial `vision` call, use `follow_up` with the returned `agent_id` to ask additional questions about the same image. The provider's server-side prompt cache makes follow-ups cheap.

The `vision` tool is only registered when `sub_agent.agents.vision.model` is configured. It requires a vision-capable model:

```yaml
sub_agent:
  agents:
    vision:
      model: claude-sonnet-4
```

---

For architecture and implementation details, see [Sub-agent Delegation Internals](sub-agent-delegation-internals.md).
