# Image handling research

This page records comparisons with other coding agents and future design options for image persistence. Current user behavior is documented in [Images](../user/images.md).

## How other coding agents handle images

### claude-code (TypeScript, Anthropic, closed source)

**Strategy: Strip after model response (same as Steiner)**

- Images are sent once, then stripped from conversation history.
- They are replaced with `[Image #N]` placeholders.
- Changelog evidence says stripped images no longer prompt the model to reread unavailable media.
- Corrupt or zero-byte images become text placeholders.

### codex (Rust, OpenAI, codex-rs)

**Strategy: Resend every turn, never strip**

- Images persist in in-memory history and are resent while the model supports images.
- Non-vision models receive a text omission.
- Pre-send resize caps images at 2048px / 2500 patches.
- `context_manager/history.rs` stores `ContentItem::InputImage`; `normalize.rs` strips only without image input support.

### crush (Go/Bubble Tea, Charmbracelet)

**Strategy: Resend every turn until compaction**

- Raw image bytes are stored locally and resent on each API call.
- Compaction loses images from history and produces text only.
- Undecodable images use a placeholder.
- Token estimation uses a rough `len/4` heuristic on image metadata.

### opencode (TypeScript, anomalyco/opencode)

**Strategy: Resend every turn until compaction, by reference**

- File references are stored and converted to base64 at request time.
- Images are resent until auto-compaction.
- Compaction replaces them with an attached-image placeholder.
- There is no image-specific token estimate.

## Future considerations

If stripping after the response proves too limiting, options include keeping images for a few turns, resending until compaction, generating a description with a cheaper model, or letting users pin and dismiss images manually.
