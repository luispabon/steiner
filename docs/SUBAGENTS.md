# Sub-agent delegation

`steiner` exposes six sub-agent-as-tool operations that delegate bounded tasks to isolated child agents.

## Available agents

Sub-agent delegation is **enabled by default**. When it is, the model sees six additional tools alongside the built-in ones:

| Tool | What it does | Extra params | Can mutate? |
|------|-------------|-------------|-------------|
| `explore` | Navigate the codebase to find files, symbols, call sites, and patterns | `task` only | No |
| `research` | Gather and synthesise information from the codebase or web | `task` only | No |
| `code` | Implement a scoped change — read relevant files, write changes, run tests | `task` only | Yes (`mutate`, `bash`) |
| `plan` | Analyse a sub-problem, evaluate options, and produce a structured recommendation | `task` only | No |
| `verify` | Run tests, linters, builds, or other checks and report pass or fail | `task` only | No |
| `delegate` | Generic sub-agent with full customisation | `task`, `context`, `system_prompt`, `max_turns`, `timeout` | Depends on config |

The five specialised tools (`explore`, `research`, `code`, `plan`, `verify`) are hardcoded with purpose-built system prompts and tool allowlists. The generic `delegate` tool lets you set a custom system prompt, pass extra context, and constrain turn/time budgets per invocation.

## Safety

- A sub-agent **cannot delegate further** — the `delegate` tool is always stripped from child registries.
- Only the `code` sub-agent and the generic `delegate` (when its config allows it) have access to file-mutation tools (`mutate`, `write`, `edit`) or `bash`.
- `explore`, `research`, and `plan` are read-only.
- `verify` can run commands via `bash` but must not modify files.
- The `research` agent lists `web_search` and `fetch_url` in its allowlist, but these are dummy stubs that return "not yet implemented" — they exist as scaffolding for future work.
- All sub-agent tools are automatically approval-gated as `auto` — no manual prompt is needed to use them.

## Configuration

Sub-agents are configured under the `sub_agent` key in `config.yaml`:

```yaml
sub_agent:
  # Master switch — set to false to remove all sub-agent tools from the model.
  enabled: true

  # Default limits for all sub-agents. The code also applies a floor of 15.
  max_turns: 30
  max_tokens: 100000

  # Tools available to the generic `delegate` sub-agent.
  # Specialised agents (explore/research/code/plan/verify) use their own
  # hardcoded allowlists and ignore this field.
  allowed_tools:
    - read
    - glob
    - grep
    - ls
    - write
    - edit
    - bash
    - scratchpad

  # Per-agent-type model overrides. When set, sub-agents of that type use
  # a different model than the parent agent.
  agents:
    code:
      model: gpt-4o
    research:
      model: claude-sonnet-4
```

Each entry under `agents` keyed by agent type name can set `model` to any model alias defined in your `models` configuration. If no override is set, the sub-agent uses the same model as the parent.

### Per-invocation overrides (generic `delegate` only)

When calling the `delegate` tool, the model can pass `max_turns` and `timeout` to tighten limits for that single invocation. These follow **tighten-only** semantics — they can only reduce limits, never raise them above the configured default.

## Default tool allowlists

| Agent | Tools available |
|-------|----------------|
| `explore` | `read`, `glob`, `grep`, `ls`, `scratchpad` |
| `research` | `read`, `glob`, `grep`, `ls`, `web_search`*, `fetch_url`*, `scratchpad` |
| `code` | `read`, `glob`, `grep`, `ls`, `mutate`, `bash`, `scratchpad` |
| `plan` | `read`, `glob`, `grep`, `ls`, `scratchpad` |
| `verify` | `read`, `glob`, `grep`, `ls`, `bash`, `scratchpad` |

\* `web_search` and `fetch_url` are not yet implemented.
