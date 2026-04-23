## Question
What current, authoritative guidance should steer Steiner's move from turn-count compaction to token-budget-based context compaction, specifically for:

1. Discovering or inferring a provider's context window / max input tokens.
2. Estimating tokens for chat/message payloads, especially tool-heavy conversations.
3. Reserving headroom for completion tokens and tool overhead.
4. Handling caveats in OpenAI-compatible backends.

## Findings
- The model context window is a model-level property, not something you can reliably infer from a request alone. OpenAI's conversation-state docs define the context window as the maximum number of tokens in a single request and direct you to model details for the exact value. OpenAI model cards now list context window and max output tokens on the model page, so the practical source of truth is per-model metadata rather than a generic API field. Anthropic's context-window docs make the same distinction and describe context window size as model-specific working memory.
- For providers that expose a token-count endpoint, use it. Anthropic's `messages.count_tokens` endpoint accepts the same structured message shape as message creation, including system prompts, tools, images, and PDFs, and returns `input_tokens`. Anthropic explicitly says the count is an estimate and can differ slightly from the actual message creation count. That is a useful precedent for Steiner even if the backend is not Anthropic.
- For OpenAI-style chat payloads, token counting is message-structure-aware, not just text-length-aware. OpenAI's tokenizer guide says each message consumes tokens for content, role, and other fields, plus extra formatting overhead that may change by model. Their cookbook also shows separate accounting for tool-heavy prompts: tool schemas add a non-trivial fixed overhead beyond the plain message text, and the cookbook warns that function/tool input consumes extra tokens on top of the base message estimate.
- Tool-heavy conversations are where naive estimators fail fastest. OpenAI's cookbook example for tool calls shows prompt token counts changing materially once tool schemas are included. Anthropic's token-counting guide similarly shows a weather tool example jumping to hundreds of input tokens, which is a strong signal that Steiner must count tool definitions every turn, not just the visible user/assistant text.
- Completion headroom needs to be reserved explicitly. OpenAI's Chat Completions reference says `max_completion_tokens` is an upper bound for visible output plus reasoning tokens, while `max_tokens` is deprecated and not compatible with o-series models. OpenAI's reasoning docs say reasoning tokens occupy context-window space and can make a response incomplete even when the visible completion is short. The Responses API uses `max_output_tokens` with the same "visible + reasoning" semantics. Bottom line: the budget must reserve room for output and hidden reasoning, not just the prompt.
- OpenAI's docs also point to manual or server-side compaction as an advanced context-management tool. Their Responses API now has a `/responses/compact` flow that produces a compacted window for the next turn, but that is specific to the Responses API and is stateless. That is useful design guidance, but Steiner currently uses chat-completions-style requests against OpenAI-compatible backends, so it cannot rely on this endpoint unless it switches APIs.
- OpenAI-compatible backends are not uniform. Ollama's compatibility docs explicitly say the OpenAI API does not provide a way to set context size; in Ollama, context length is configured in a `Modelfile` via `PARAMETER num_ctx`, then exposed under a model name. Ollama also documents that only parts of the OpenAI API are supported, and that the Responses API support is partial. This means Steiner cannot assume `base_url` or `model` alone tells it the available context window, and it cannot assume all OpenAI request fields are honored.
- Inference: because `usage` is only available after a request, Steiner should treat backend-reported usage as feedback, not as the budget source of truth. The budget source of truth needs to be config plus estimator. Actual usage can then be used to calibrate or log drift.

## Implications
- The current single-provider config is too narrow for the feature you want. The new `providers:` map plus `default_model` is the right direction because context size, completion limit, and transport settings are model-specific.
- Steiner needs two separate numbers in the request planner:
  - estimated prompt/input tokens before the request
  - reserved output headroom derived from the model's completion limit and a safety margin
  These should be combined into a trigger such as: compact before `estimated_prompt_tokens + reserved_output_tokens + tool_headroom` reaches the model's context window.
- The current turn-count compaction logic is the wrong abstraction. It should be replaced by request-budget logic that can compact based on estimated token pressure, even if the conversation has only a few turns but one huge tool dump.
- Tool schemas, tool outputs, and retained summaries must be counted as first-class prompt contributors. If Steiner keeps treating them as "just text", compaction will trigger too late on tool-heavy sessions.
- The compaction threshold should be conservative. Docs consistently describe token counts as estimates, and reasoning/tool overhead is variable. A hard cutoff at the model limit is too late; Steiner should leave a margin that accounts for:
  - visible completion tokens
  - hidden reasoning tokens where supported
  - tool-call/schema overhead
  - estimator error
- For OpenAI-compatible backends, Steiner should probably support an explicit per-model `context_size` config even when the backend may have a different internal limit. That is safer than trying to auto-discover the limit from the API, because the API may not expose it.

## Risks and Uncertainties
- There is no universal, exact, portable token estimator across all OpenAI-compatible backends. Some backends may use different tokenizers or serialize messages differently.
- OpenAI docs themselves warn that chat token counting is model-dependent and can change over time. That makes any local estimator approximate by design.
- Tool overhead can be much larger than expected and can vary by model and schema complexity. A margin that is too small will produce late compactions and occasional context overflows.
- Some backends may omit or partially implement `usage` fields, `max_completion_tokens`, or even tool-call behavior. Steiner should not make those fields mandatory for correctness.
- If the compaction summary itself becomes large, it can eat into the same token budget and force repeated compactions. That means the summary format needs its own bound.

## Sources
- [OpenAI conversation state](https://platform.openai.com/docs/guides/conversation-state)
- [OpenAI reasoning models](https://platform.openai.com/docs/guides/reasoning)
- [OpenAI Chat Completions reference](https://platform.openai.com/docs/api-reference/chat/create-chat-completion)
- [OpenAI cookbook: How to count tokens with Tiktoken](https://cookbook.openai.com/examples/how_to_count_tokens_with_tiktoken)
- [OpenAI model pages](https://developers.openai.com/api/docs/models)
- [Anthropic token counting](https://docs.anthropic.com/en/docs/build-with-claude/token-counting)
- [Anthropic context windows](https://docs.anthropic.com/en/docs/build-with-claude/context-windows)
- [Ollama OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility)

## Open Questions
- Should Steiner treat `context_size` as total window size, or as a prompt-only limit after reserving a fixed completion headroom?
- Should the estimator be a pure heuristic, or should it use per-provider counting endpoints when available and fall back locally otherwise?
- Should the first compaction pass preserve all prior user messages verbatim and summarize only assistant/tool content, or should it summarize the whole conversation into a single synthetic block?
- How conservative should the default safety margin be for reasoning-capable models versus plain chat models?
- Do we want backend-specific overrides for tokenization/serialization quirks, or one shared estimator with a calibration factor?
