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

## Large conversations

The advisor trims older ordinary tool output when the complete advisor request reaches `advisor.context_reduction_threshold_tokens`. The default is `64000`; `0` disables reduction; any other value must be at least `48000`. The trigger is soft and measured with the advisor model's token estimator. When 60% of the advisor model's input budget (context window minus the room reserved for its reply and estimator padding) is smaller than the configured value, that smaller figure becomes the effective trigger.

The trigger is a target for request size, not a ceiling: a conversation whose protected context is already large stays large.

Reduction preserves the system prompt, user messages (including asynchronous delegation results), assistant prose and tool-call structure, `sub_agent`, `follow_up`, and `mutate` results, prior `advisor` notes, the caller's question, and explicitly requested files. `mutate` receipts stay intact because they are the record of what changed on disk, and a tool result whose tool name cannot be resolved is left alone. It only changes older ordinary tool results: an exact duplicate full read is replaced by a note referencing the earlier read, long head and tail clips replace older output, and stubs remain for the oldest results. If the earlier read is itself reduced, the note becomes a self-contained omission stub instead of pointing at content that is no longer present. A recent tail of tool output is always kept.

Frozen replacements replay unchanged on later calls, so they are not planned or clipped again as the conversation grows. A call below the trigger still replays them; the trigger gates new batches, not replay. A new batch is planned only at or above the trigger, and it must save at least a tenth of the trigger before it is applied. An attempt that saves less changes nothing and waits until the request grows by another tenth of the trigger, measured against the same conversation; compaction, a rewrite of older messages, or a shrink starts planning over.

A batch only replaces older results, so the cacheable prefix is unchanged up to the first changed result and only the rest of the request is resent uncached. Frozen replay is stable between batches, so consecutive calls reuse the same prefix. Input size alone does not predict cost: cache reads, a batch that invalidates part of the prefix, and the advisor's own output all count. Judge reduction with the existing usage and cache statistics ([Usage](usage.md)) and the token estimates on the `advisor_complete` event instead of assuming a smaller request is cheaper.

## Cost and safety

- Each call sends the caller's conversation to the advisor model, so calls late in a long session can be large and slow. The caps bound cost and latency; `advisor.timeout` applies to each attempt. Context reduction is a soft input-size target, not a spending or request ceiling: it never rejects a call, and protected context can keep a request large. It limits growth; it does not replace the caps.
- The advisor has no tools and cannot change local state.
- It uses the credentials of the provider configured for the advisor model.
