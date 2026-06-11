# Image Paste Feature

How to use images in steiner, and how steiner manages images in conversation context.

## Using Image Paste

In the interactive TUI, use **Ctrl+V** to paste an image. Steiner reads the image from your clipboard or from a file path. Images are automatically:

- **Resized** to a maximum of 2048px on the longest side to keep token costs manageable
- **Token-accounted** using the formula `(width × height) / 750 + 85` overhead per image
- **Displayed** as `[Image N]` in the composer until you submit

You can paste multiple images before submitting — they accumulate and are all sent with your message. Use `/clear` in the TUI to dismiss pending images without sending them.

**Supported formats:** PNG, JPG, JPEG, GIF, WebP  
**Max size:** 5MB per image

### What Happens After the Model Responds

After the model processes your message and responds, image data is automatically removed from the conversation history and replaced with a text placeholder like `[image: 2560x1545 png 478KB]`. This keeps the conversation context lean — a single image can cost 5K-160K tokens, and re-sending the same image across many turns compounds the cost quickly.

If you need the model to re-examine an image in a follow-up message, simply paste it again.

### Vision Capability

If your model doesn't support vision (configured with `vision: false`), images are automatically stripped before being sent to the model. The placeholder remains in your message history for reference.

## Implementation Details: Strip After Model Response

Steiner's approach is the most token-efficient strategy for local and cost-conscious LLMs:

**Flow:**
1. User pastes image → held as pending `ImageBlock` in TUI
2. User submits message → image attached to `agent.Message`, sent in API request
3. Model responds → image data stripped from the message, replaced with text placeholder (e.g., `[image: 2560x1545 png 478KB]`)
4. Subsequent turns → only the placeholder text is in history, model knows an image was discussed but cannot re-examine it

**Trade-off:** The model cannot re-examine the image in follow-up turns without re-pasting. This is a conscious choice to save tokens and keep context lean.

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
