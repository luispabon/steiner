## Question

What are the exact wire formats for sending images via Anthropic and OpenAI APIs, does Bubble Tea support clipboard image paste, and how can model vision capability be detected?

## Findings

### Anthropic Messages API — Image Content Blocks

Image content block structure:

```json
{
  "type": "image",
  "source": {
    "type": "base64",
    "media_type": "image/jpeg",
    "data": "<raw-base64-no-prefix>"
  }
}
```

- Supported formats: `image/jpeg`, `image/png`, `image/gif`, `image/webp`
- Max size: 5 MB recommended
- Block placed in same `content` array as text blocks on user messages
- Alternative source type: `"type": "url"` with `"url": "https://..."` (not needed for local files)
- Base64 string is raw — no `data:` URI prefix

### OpenAI Chat Completions API — Vision

Image content block structure:

```json
{
  "type": "image_url",
  "image_url": {
    "url": "data:image/jpeg;base64,<base64-data>",
    "detail": "auto"
  }
}
```

- Supported formats: `image/jpeg`, `image/png`, `image/webp`, `image/gif` (non-animated only)
- `detail` parameter: `"auto"` (default), `"low"`, `"high"` — affects token cost
- Data URI format required (with `data:` prefix and media type)
- Format is identical across GPT-4o, GPT-4-turbo, and all vision variants

### Bubble Tea — Clipboard Image Paste

Bubble Tea v1.3.x does NOT support image clipboard paste. `PasteMsg` carries text only. The `atotto/clipboard` Go package is also text-only.

Workarounds require OS-specific shell commands:
- Linux/X11: `xclip -selection clipboard -t image/png -o`
- Wayland: `wl-paste -t image/png`
- macOS: `pbpaste -Prefer png`

### Vision Capability Detection

Neither Anthropic nor OpenAI expose vision capability in model metadata APIs. Standard approach is hardcoded model ID lists or a config flag.

## Implications

- Provider wire layer needs two different image content block shapes — Anthropic uses nested `source` object with raw base64, OpenAI uses `data:` URI in `image_url`
- Message types need a structured content block model (currently text-only strings)
- Tool results currently collapse to string via `ToolResultEnvelope.Content` — image data from `read` tool needs a sideband channel
- Vision detection should use a config flag (`vision: true/false` in `ModelConfig`) rather than hardcoded lists, keeping maintenance with the user
- Clipboard image paste is deferred to a follow-up due to platform-specific external tool dependencies

## Risks and Uncertainties

- Large images (5MB base64 ≈ 6.7MB in payload) could hit HTTP payload limits on some providers
- Image token cost varies significantly by provider and detail level — no way to predict before sending
- Hardcoded vision model lists rot quickly; config flag approach is more maintainable
- `ToolResultEnvelope` → `agent.Message.Content` pipeline is string-only; image data from tool results needs architectural change

## Sources

- Anthropic Messages API documentation (image content blocks)
- OpenAI Chat Completions API documentation (vision)
- charmbracelet/bubbletea v1.3.10 source
- charmbracelet/bubbles textarea component

## Open Questions

- Should image content blocks carry estimated token cost metadata for budget tracking?
- Should there be a per-message or per-conversation image count limit?
