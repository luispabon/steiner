## Question

How do existing terminal coding agents (opencode, codex, claude-code, crush) implement Ctrl+V clipboard image paste? What patterns should steiner follow?

## Findings

### opencode (TypeScript, anomalyco/opencode)

- **TUI framework**: opentui + SolidJS (custom terminal UI)
- **Paste detection**: `onPaste` handler for bracketed paste + `prompt.paste` keymap command as fallback when bracketed paste delivers empty content (Windows Terminal <1.25)
- **Clipboard access**: Shells out to OS tools — `osascript` (macOS, reads `clipboard as "PNGf"`), `powershell.exe` (Windows/WSL), `wl-paste -t image/png` (Wayland), `xclip -selection clipboard -t image/png -o` (X11). Text fallback via `clipboardy` npm package
- **Async**: Fully async (`async/await` on promisified `execFile`/`spawn`)
- **Format**: All platforms extract PNG
- **Fallback**: All subprocess calls `.catch(() => Buffer.alloc(0))` — empty buffer falls through to next probe, eventually to text, then no-op. No user-facing error
- **Size limits**: None found in source
- **Representation**: `FilePart` with `data:image/png;base64,...` URL; `[Image N]` extmark placeholder in textarea

### codex (Rust, openai/codex — codex-rs TUI)

- **Paste detection**: Two mechanisms — `Event::Paste(String)` from crossterm for bracketed paste, plus `PasteBurst` state machine (~200 lines) for non-bracketed terminals (Windows) that heuristically detects rapid character bursts
- **Clipboard access**: `arboard` crate (native OS APIs, no shell-out). WSL fallback spawns PowerShell to dump clipboard image
- **Async**: Synchronous in event dispatch path (`arboard` is blocking I/O)
- **Format**: Always re-encodes to PNG via `image` crate regardless of source format
- **Fallback**: `PasteImageError` enum; WSL PowerShell fallback; `ClipboardUnavailable` on Android. Also checks if pasted text is a file path → opens as image if `image_dimensions()` succeeds
- **Size limits**: Dimension-based resize (max 2048px dimension, max 2500 patches). Too-large → text placeholder
- **Representation**: `[Image #N]` placeholder in textarea; `AttachedImage{placeholder, path}` sidecar. Converted to `ContentItem::InputImage{image_url: data_url, detail}` on submit

### claude-code (TypeScript, anthropics/claude-code — closed source)

- **Paste detection**: Explicit Ctrl+V keybinding; Alt+V on WSL
- **Clipboard access**: `wl-paste`/`xclip`/`xsel` on Linux; PowerShell interop on WSL
- **Async**: Yes — shows "Pasting…" footer hint during clipboard read
- **Format**: Likely PNG (screenshots confirmed)
- **Fallback**: "no image found" hint shown; corrupt/zero-byte images become text placeholders
- **Size limits**: Not documented; likely API-enforced
- **Representation**: `[Image #N]` placeholder inline; sent as content block

### crush (Go/Bubble Tea, charmbracelet/crush — formerly opencode-ai/opencode)

- **Paste detection**: `key.Matches(msg, PasteImage)` on `tea.KeyMsg` for Ctrl+V. Bracketed paste (`tea.PasteMsg`) used only for large text, not image detection
- **Clipboard access**: `github.com/aymanbagabas/go-nativeclipboard` v0.1.3 — purego (no cgo), calls native OS APIs directly. No shell-out
- **Async**: Yes — `pasteImageFromClipboard` is a `tea.Cmd` (runs in goroutine)
- **Format**: Raw bytes from clipboard; MIME detected via `net/http.DetectContentType` on first 512 bytes
- **Fallback**: Image read fails → try text as file path (`os.Stat`) → give up silently. Unsupported platforms (arm, 386, iOS, Android) return `errClipboardPlatformUnsupported` via build-tag stub
- **Size limits**: 5MB hard cap (`common.MaxAttachmentSize`)
- **Representation**: `message.Attachment{FilePath, FileName, MimeType, Content []byte}` — raw bytes, no base64 at attachment layer

## Implications

1. **`go-nativeclipboard` is the Go path**: crush proves this works in Go/Bubble Tea without cgo. No external tool dependencies to worry about. Steiner should use this instead of shelling out to `xclip`/`wl-paste`/`pbpaste`.

2. **Async via `tea.Cmd` is the consensus**: 3 of 4 implementations are async. Crush's pattern (`tea.Cmd` returning `message.Attachment`) maps directly to Bubble Tea. Steiner should follow this.

3. **Ctrl+V as `key.Binding`**: crush intercepts Ctrl+V via `key.Matches` on `tea.KeyMsg`, NOT via `tea.PasteMsg`. This is the right approach — `tea.PasteMsg` is for bracketed-paste text only.

4. **`[Image N]` placeholder pattern**: Used by opencode, codex, and claude-code. Crush uses an attachment sidecar instead. Either works; placeholder gives better user visibility.

5. **5MB limit**: crush and opencode's file picker both use 5MB. Matches steiner's existing `readImageFile` limit.

6. **MIME detection via stdlib**: `net/http.DetectContentType` on raw bytes — no extra deps needed.

7. **Platform fallback via build tags**: crush uses build-tag stubs for unsupported platforms. Clean pattern for steiner.

8. **Text-as-path fallback**: Both crush and codex check if pasted text is a file path pointing to an image. Nice UX but adds complexity — could defer.

## Risks and Uncertainties

- **`go-nativeclipboard` maturity**: v0.1.3 — pre-1.0. Works for crush but may have edge cases. purego approach avoids cgo but depends on runtime FFI
- **Wayland support in `go-nativeclipboard`**: Need to verify it handles Wayland clipboard (not just X11) — crush's build tags suggest Linux is supported but doesn't distinguish X11 vs Wayland
- **Build tag gating**: steiner would need `clipboard_supported.go` / `clipboard_not_supported.go` split, adding build complexity
- **WSL**: None of the Go implementations (crush) have explicit WSL PowerShell fallback — that's only in codex (Rust) and claude-code (TS). May be a gap

## Sources

- opencode: https://github.com/anomalyco/opencode — `packages/tui/src/clipboard.ts`, `packages/tui/src/prompt/index.tsx`
- codex: https://github.com/openai/codex — `codex-rs/tui/src/clipboard_paste.rs`, `codex-rs/tui/src/bottom_pane/chat_composer.rs`
- claude-code: https://github.com/anthropics/claude-code — CHANGELOG.md (closed source)
- crush: https://github.com/charmbracelet/crush — `internal/ui/model/clipboard*.go`, `internal/ui/model/keys.go`

## Image Token Accounting and Context Management

### The steiner bug

`token_estimator.go:countMessage()` counts `message.Content` text but **ignores `message.Images` entirely**. Base64 image data is sent on the wire (via `anthropic_wire.go:toAnthropicMessage()`) but the budget system is blind to it. A 478KB PNG → ~637KB base64 → Anthropic sees ~160K tokens → immediate context overflow and unrecoverable compaction failure.

### How others handle it

| Feature | crush | opencode | codex |
|---|---|---|---|
| Image token formula | `len/4` heuristic on metadata text (not base64 data) | None — relies on API-reported actual usage | OpenAI 32px-patch budget formula |
| Pre-send resize | No | No | Yes (`max_dimension: 2048`, `max_patches: 2500`) |
| Drop on compaction | Implicit via summarize (images lost) | Implicit via session reset at 95% fill | Placeholder substitution on error |
| Separate image budget | No | No | No (capped by resize) |
| Image-specific rules | `SupportsImages` gate strips images if model doesn't support them | None | detail level enforcement + model downgrade |

### Key observations

1. **Nobody does image token estimation well**: crush uses a trivial heuristic, opencode punts to the API, codex physically resizes to stay within bounds. None have accurate pre-send image token counting.

2. **Anthropic's image token formula**: `tokens ≈ (width × height) / 750` for standard images. This is documented and predictable. Steiner should use this for pre-send estimation.

3. **Compaction must handle images**: When compacting, image data in older messages should be replaced with a text placeholder (e.g., `[image was here: 2560x1545 png 478KB]`). This recovers the tokens without losing the conversational context that an image was discussed.

4. **Pre-send resize is the safest approach**: codex's approach of capping dimensions before sending is the most robust. A 2560×1545 screenshot at full resolution costs ~5270 tokens via Anthropic's formula — manageable. But a 4K screenshot (3840×2160) would cost ~11K tokens. Resize to max 2048px longest side as a safety cap.

### Recommendations for steiner

1. **Add image token estimation to `countMessage()`**: Use `(width × height) / 750` plus base64 overhead. This makes the budget system aware of images.

2. **Strip images on compaction**: Replace `ImageBlock` data with text summary in compacted messages. Keep the placeholder so the model knows an image was discussed.

3. **Cap image dimensions on paste**: Resize images exceeding 2048px longest side before base64 encoding. Keeps token cost predictable.

4. **`SupportsImages` gate**: Don't send images to models that don't support vision. Already have the `Vision` config field — wire it up.

## Resolved Questions

1. ~~Does `go-nativeclipboard` work reliably under Wayland?~~ → Accept risk; crush uses it on Linux without X11/Wayland distinction
2. ~~WSL PowerShell fallback?~~ → **Deferred** (user decision)
3. ~~Text-as-path fallback?~~ → **Included** (user decision)

## Open Questions

1. Should pre-send resize be included in this plan or deferred?
2. What max dimension should be used for resize? (2048px matches codex's HIGH_DETAIL_LIMITS)
