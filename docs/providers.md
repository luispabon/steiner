# Provider notes

## KV cache behaviour

Most inference providers cache the key-value (KV) state of the transformer for
a prefix of the input. Steiner's prompt assembly is designed to maximise cache
hit rate by placing stable content first.

### Anthropic (Claude)

- Caches at both system prompt and messages array level.
- System prompt prefix is cached automatically; no explicit cache_control needed.
- Messages array prefix is cached turn-to-turn as long as earlier messages are
  unchanged.
- **Implication**: steiner's two-zone structure (stable system prompt + volatile
  conversation) gets full benefit. Both zones can be cached.

### OpenAI (GPT-4o, o-series)

- Caches the full prompt prefix automatically (system + messages combined).
- Cache hit requires the prefix to be byte-identical.
- **Implication**: same as Anthropic. Stable system prompt + stable earlier
  conversation turns benefit from caching.

### Ollama (local models)

- Most Ollama-served models cache the system prompt prefix only.
- The messages array is typically not prefix-cached turn-to-turn.
- **Implication**: maximise stable content in the system prompt. Conversation
  history will be reprocessed each turn regardless. Do not attempt to move
  stable content into the messages array for caching — it will not help.
- **Alternation constraint**: Ollama enforces strict user/assistant alternation
  in the messages array. Consecutive messages with the same role are rejected.
  Steiner merges consecutive same-role user blocks at render time to satisfy
  this constraint.

### Gemini (Google AI / Vertex)

- Explicit context caching via API; not automatic prefix caching.
- Steiner does not yet use the explicit cache API.
- **Implication**: no automatic cache benefit. Treat as full reprocessing per
  turn until explicit cache support is added.
