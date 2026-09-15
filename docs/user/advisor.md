# Advisor sub-agent

`advisor` is Steiner's optional stronger-model steering tool. It lets the main agent ask a configured advisor model for planning and risk guidance without handing work to a child agent.

Despite the filename, the advisor is not a sub-agent in the delegation sense. It does not run a child loop, receive tools, mutate files, or maintain its own session. It is one read-only model call over the parent conversation.

## Purpose

Use the advisor when a stronger model can improve the main loop's judgment:

- before committing to an implementation or review approach
- when requirements are ambiguous or tradeoffs matter
- after verification failures when the next fix path is unclear
- before declaring a plan, implementation, or review complete

The advisor is for strategic steering. It is not a code executor, verifier, reviewer of hidden child-agent work, or replacement for local tests.

## Configuration

The advisor is disabled by default.

```yaml
advisor:
  enabled: true
  max_uses_per_run: 2
  max_tokens: 256
  timeout: 5m

providers:
  local:
    type: ollama
    base_url: http://localhost:11434/v1

models:
  profiles:
    default:
      default_model: local/<default-model-id>
      advisor: local/advisor-model
```

Fields:

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enables the advisor tool and advisor prompt steering. |
| `max_uses_per_run` | `3` | Per-session call cap, enforced across every turn in the process. Required to be at least `1` when enabled. |
| `max_tokens` | `nil` | Optional output-token limit forwarded to the advisor provider request. |
| `timeout` | `180s` | Optional HTTP timeout override applied only to advisor calls. |

The advisor model is assigned in the selected profile's `models.profiles.<name>.advisor` field. When omitted or empty, it falls back to that profile's `default_model`. Model references use the same model configuration and provider discovery path as other runtime models.

## Tool behavior

The model-facing tool is named `advisor`. Its schema accepts two optional properties: `question` (free text describing what to judge) and `files` (an array of workspace paths to include verbatim, since the advisor has no tool access and cannot read files itself). Both are optional. A bare call with neither is a pure timing signal over the live conversation.

The cap is per session, not per turn. Calls within `max_uses_per_run` invoke the advisor model. Calls after the cap return:

```text
advisor budget exhausted for this session (N/N); proceed on your own judgment
```

The tool definition stays registered for the whole run, including after the cap is reached. This keeps provider-visible tool schemas stable.

## Relationship to delegation

Advisor and delegation solve different problems.

| Capability | Advisor | Delegation |
|------------|---------|------------|
| Execution shape | One model call | Child agent loop |
| Context | Live parent conversation | Explicitly passed task and context |
| Tools | None | Per-agent allowlist |
| Mutation | Never | Only for mutation-capable agents such as `code` |
| Parent transcript impact | Advisor result is appended | Only bounded child result summary is appended |
| Best use | Steering, tradeoffs, residual risk | Exploration, implementation, verification, research |

The advisor cannot see a delegate's private internal transcript unless the delegate result summary is already present in the parent conversation.

## Events and UI

Advisor calls emit structured lifecycle events:

| Event | Meaning |
|-------|---------|
| `advisor_started` | Advisor model call is starting. |
| `advisor_complete` | Advisor model call completed or failed. |
| `advisor_budget_exhausted` | The per-run cap was reached and no provider call was made. |

The TUI renders advisor activity as a collapsible advisor panel with model and use-count metadata.

## Safety and limits

- Advisor is opt-in and disabled by default.
- Advisor has no tool access and cannot mutate local state.
- Advisor guidance is advisory only; the main agent must adapt it to local evidence.
- The per-run cap limits cost and latency.
- Provider credentials are those configured for the selected advisor model; do not hardcode secrets in config.

For payload shaping, cache behavior, and package ownership, see [Advisor internals](../internals/advisor.md).
