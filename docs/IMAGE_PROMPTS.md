# Image Context Management

How steiner handles images in conversation context, and how other coding agents approach the same problem.

## Steiner's Approach: Strip After Model Response

Images are sent to the model once, then replaced with a text placeholder on the next turn. This is the most token-efficient approach — a single image can cost ~5K-160K tokens depending on resolution, and re-sending it every turn compounds quickly.

**Flow:**
1. User pastes image → held as pending `ImageBlock` in TUI
2. User submits message → image attached to `agent.Message`, sent in API request
3. Model responds → image data stripped from the message, replaced with text placeholder (e.g., `[image: 2560x1545 png 478KB]`)
4. Subsequent turns → only the placeholder text is in history, model knows an image was discussed but cannot re-examine it

**Trade-off:** The model cannot re-examine the image in follow-up turns. If the user needs the model to look at the image again, they must re-paste it.

## How Other Coding Agents Handle Images

### claude-code (TypeScript, Anthropic — closed source)

**Strategy: Strip after model response (same as steiner)**

- Images are sent once, then stripped from conversation history
- Replaced with `[Image #N]` placeholders
- Changelog evidence: *"Fixed stripped images prompting the model to repeatedly re-read media that was no longer present"* — confirms images are removed after processing
- Corrupt/zero-byte images become text placeholders rather than crashing

### codex (Rust, OpenAI — codex-rs)

**Strategy: Re-send every turn, never strip**

- Images persist in in-memory history indefinitely
- Re-sent on every API call as long as the model supports images
- Only stripping: for non-vision models, images are replaced with `"image content omitted because you do not support image input"`
- No eviction, no time-based expiry, no compaction-driven removal
- Pre-send resize caps images at 2048px / 2500 patches to limit per-image token cost
- Implementation: `context_manager/history.rs` stores all `ContentItem::InputImage`; `normalize.rs` strips only when `InputModality::Image` is absent

### crush (Go/Bubble Tea, Charmbracelet — formerly opencode-ai/opencode)

**Strategy: Re-send every turn until compaction**

- Raw image bytes stored in local database as `BinaryContent{Data []byte, MIMEType string}`
- Converted to `fantasy.FilePart` on each API call — re-sent every turn
- On compaction/summarize: images lost from history (summarize produces text only)
- Fallback: `"[Image data could not be loaded]"` placeholder for undecodable images during session replay
- Token estimation uses rough `len/4` heuristic on image metadata text (not on actual image data)

### opencode (TypeScript, anomalyco/opencode)

**Strategy: Re-send every turn until compaction (by reference)**

- Stores file references (URI/path) in message history, not inline base64
- Converts to base64 at API request time by re-reading the URI
- Re-sent every turn until auto-compact triggers at 95% context usage
- On compaction: images stripped to `[Attached image/png: filename]` text placeholder
- No image-specific token estimation — relies on API-reported actual usage

## Comparison

| Aspect | claude-code | codex | crush | opencode | **steiner** |
|--------|-------------|-------|-------|----------|-------------|
| Re-sent each turn? | No (one-shot) | Yes (always) | Yes (raw bytes) | Yes (by URI) | No (one-shot) |
| Stripped when? | After model response | Never (vision models) | On compaction | On compaction at 95% | After model response |
| Placeholder format | `[Image #N]` | Text string | `[Image data...]` | `[Attached ...]` | `[image: WxH fmt size]` |
| Pre-send resize? | Unknown | Yes (2048px cap) | No | No | Yes (2048px cap) |
| Image token estimation | Unknown | OpenAI patch formula | `len/4` heuristic | None (API-reported) | `(w×h)/750` Anthropic formula |
| Vision gating | Unknown | Yes (strip for non-vision) | Yes (`SupportsImages`) | No | Yes (config field) |

## Future Considerations

If the strip-after-response approach proves too limiting (users frequently need the model to re-examine images), consider:

1. **Keep for N turns**: Hold image for 2-3 turns before stripping. Allows follow-up questions about the image without re-pasting.
2. **Re-send until compaction**: Like crush/opencode. Simpler, but expensive for large images over many turns.
3. **Describe-then-discard**: Send image to a cheap/fast model to generate a detailed text description, store only that. No existing agent does this, but it would be the most token-efficient while preserving some image understanding.
4. **Hybrid with user control**: Let users pin an image to keep it in context, or manually dismiss it. More complex UI.
