# Advisor

The advisor lets the agent ask a stronger model for strategic guidance mid-task. It is one model call over the live conversation: no tools, no child loop, no file changes. It is not a sub-agent; for delegated work see [Sub-agent delegation](sub-agent-delegation.md).

## When it helps

- before committing to an implementation or review approach
- when requirements are ambiguous or tradeoffs matter
- after a failed check when the next fix is unclear
- before declaring a plan, implementation, or review complete

The advisor steers; it does not replace tests, reviews, or local evidence. The agent decides what to do with its answer.

## Enabling it

The advisor is off by default in interactive and `--exec` runs:

```yaml
advisor:
  enabled: true

models:
  profiles:
    default:
      default_model: openai/<default-model-id>
      advisor: anthropic/<stronger-model-id>
```

The profile's `advisor` model falls back to its `default_model`. Oneshot phases always have the advisor available, whatever `advisor.enabled` says. Caps, output-token limit, and timeout are listed under [Configuration](configuration.md#advisor-block).

## Who can call it

- The main agent.
- `code`, `documentor`, `review`, and `evaluate` sub-agents. `explore`, `research`, `sanity_check`, and `vision` cannot.

The agent can pass a `question` and a list of `files` to include verbatim, since the advisor cannot read files itself. Both are optional; a bare call asks for guidance on the conversation as it stands.

## Usage caps

- `advisor.max_uses_per_run` caps calls by the main agent across the whole session (default `3`).
- `advisor.max_uses_per_sub_agent` caps calls by each sub-agent over its lifetime, including after `follow_up` (default `1`).

Once a cap is reached, further calls do not reach the model. The agent gets:

```text
advisor budget exhausted for this session (N/N); proceed on your own judgment
```

The tool stays listed after the cap, so the available tools do not change mid-session.

## In the TUI

Each advisor call shows as a collapsible advisor panel with the model and the use count.

## Cost and safety

- Each call sends the caller's conversation to the advisor model, so calls late in a long session can be large and slow. The caps bound cost and latency; `advisor.timeout` applies to each attempt.
- The advisor has no tools and cannot change local state.
- It uses the credentials of the provider configured for the advisor model.
