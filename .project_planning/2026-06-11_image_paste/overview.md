## Request

Add clipboard image paste (Ctrl+V) to the TUI, with proper image token accounting, compaction handling, and pre-send resize so images don't blow the context window.

## Overview

Three interconnected pieces:

**1. Clipboard image paste**: Intercept Ctrl+V in the TUI composer via `key.Binding`. Run clipboard read asynchronously as a `tea.Cmd` using `go-nativeclipboard` (purego, no cgo). If image data found, validate and attach. If clipboard has text pointing to an image file, attach that instead. Pattern follows crush (charmbracelet/crush).

**2. Image token accounting**: Fix `token_estimator.go:countMessage()` which currently ignores `message.Images` entirely. Add image token estimation using Anthropic's formula (`width × height / 750`). This makes the budget system aware of image costs so compaction triggers at the right time.

**3. Image-aware compaction and resize**: Strip image data from older messages during compaction (replace with text placeholder). Cap pasted image dimensions to 2048px longest side before base64 encoding. Gate image sending on model vision capability.

The image pipeline already exists: `agent.ImageBlock` → `provider.ImageBlock` → Anthropic/OpenAI wire format. TUI placeholder rendering exists in `internal/tui/image.go`. Vision capability is configurable per model. This task completes the image input path end-to-end.

## Key Decisions

1. **`go-nativeclipboard` for clipboard access**: purego (no cgo), native OS APIs. Proven in crush. Build-tag gated for supported platforms (linux/darwin/windows, 64-bit).

2. **Async via `tea.Cmd`**: Clipboard read runs off the UI thread. Consensus approach across crush, opencode, claude-code.

3. **Extend `SubmitPrompt` with images**: Add `Images []agent.ImageBlock` to `interactive.SubmitPrompt`. Session handler threads images into `agent.Message` on submit.

4. **TUI pending images**: Model accumulates pasted images until submit. Multiple pastes = multiple images. Clear on submit or /clear.

5. **Text-as-path fallback**: When clipboard image read fails, try clipboard text as file path. If it's an image, attach it. Both crush and codex do this.

6. **Image token estimation**: `countMessage()` adds `(width × height) / 750` per image. Makes budget system image-aware.

7. **Pre-send resize**: Cap at 2048px longest side (matches codex's `HIGH_DETAIL_LIMITS`). Keeps token cost predictable. Resize uses Go's `image` stdlib (already imported by `read.go`).

8. **Strip images after model response**: Like claude-code — send image once, then replace with text placeholder (e.g., `[image: 2560x1545 png 478KB]`) before the next turn. Prevents re-sending ~5K-160K tokens every subsequent turn. Trade-off: model can't re-examine image without re-paste. See `docs/IMAGE_PROMPTS.md` for full rationale and alternatives.

9. **Vision capability gate**: Don't send images to models where `Vision` config is explicitly `false`. Already have the field in `config.ModelConfig` — wire it through to message assembly.

10. **5MB size limit**: Consistent with existing `readImageFile` and crush.

11. **MIME detection via stdlib**: `net/http.DetectContentType` on raw bytes.

12. **Build-tag platform gating**: `clipboard_supported.go` / `clipboard_not_supported.go` with build tags.

## Tradeoffs

- **`go-nativeclipboard` vs shell-out**: Library eliminates external deps. Risk: v0.1.3 pre-1.0, but crush validates.
- **Async vs sync paste**: Async avoids potential slow clipboard reads blocking TUI. Small complexity cost.
- **Anthropic formula vs generic heuristic**: `(w×h)/750` is Anthropic-specific. OpenAI uses a different patch formula. Using Anthropic's formula since it's the primary provider; overestimates are safer than underestimates.
- **Resize before encode vs after**: Chose before — smaller base64, fewer tokens, less memory. Downside: lossy for images where original resolution matters. 2048px cap is generous enough for screenshots and diagrams.
- **Strip after response vs re-send every turn**: Chose strip-after-response (claude-code's approach). Re-sending is simpler but a single image can cost 5K-160K tokens per turn. codex, crush, opencode all re-send — but they have larger context windows or external resize. Steiner strips aggressively. See `docs/IMAGE_PROMPTS.md` for alternatives if this proves too limiting.
- **WSL PowerShell fallback**: Deferred. No Go implementation has this yet. Add if users report issues.

## Scope Boundaries

**In scope**:
- Ctrl+V interception in TUI composer via `key.Binding`
- `go-nativeclipboard` integration with build-tag platform gating
- Async clipboard image read as `tea.Cmd`
- Text-as-path fallback (clipboard text → file path → image attachment)
- Image validation (5MB size limit, MIME detection)
- Pre-send resize (2048px longest side cap)
- Pending image state in TUI Model with `[Image N]` placeholder
- `SubmitPrompt` extension to carry images
- Session handler threading images into `agent.Message`
- Image token estimation in `countMessage()` using `(w×h)/750`
- Image data stripping after model response (one-shot send, then placeholder)
- `docs/IMAGE_PROMPTS.md` reference doc for image context strategies
- Vision capability gating on message assembly
- Unit tests for all new behavior

**Out of scope**:
- Drag-and-drop (terminal limitation)
- Remote URL paste / auto-download
- Image editing
- WSL PowerShell fallback (deferred)
- OpenAI-specific image token formula (use Anthropic's; safe overestimate)

## Verification Strategy

| Command | Cost | Notes |
|---------|------|-------|
| `gofmt -w <files>` | cheap | After every Go edit |
| `goimports -w <files>` | cheap | After every Go edit |
| `go build ./...` | cheap | Compile check |
| `go test ./internal/tui/ -run <TestName>` | cheap | TUI clipboard + paste tests |
| `go test ./internal/interactive/ -run <TestName>` | cheap | Session submit + image tests |
| `go test ./internal/provider/ -run <TestName>` | cheap | Token estimation + wire tests |
| `go test ./internal/agent/ -run <TestName>` | cheap | Compaction + image strip tests |
| `go test ./...` | medium | Full test suite |
| `go vet ./...` | cheap | Static analysis |
| `make check` | medium | Full CI-equivalent |

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | `go-nativeclipboard` over shell-out | Proven in crush; no external deps; purego |
| 2 | Async via `tea.Cmd` | Consensus (3/4 async); natural Bubble Tea pattern |
| 3 | Extend SubmitPrompt with Images field | Minimal API change; images are part of prompt |
| 4 | Include text-as-path fallback | crush + codex do this; good UX |
| 5 | Include pre-send resize at 2048px | Prevents context blow-up from large images |
| 6 | Image token estimation `(w×h)/750` | Makes budget system image-aware; Anthropic formula |
| 7 | Strip images after model response | claude-code approach; prevents 5K-160K token/turn waste |
| 8 | Vision gate on message assembly | Don't waste tokens sending images to text-only models |
| 9 | Same plan for paste + token fixes | Image paste without token awareness is broken by default |
| 10 | Defer WSL PowerShell fallback | No Go impl exists; add if needed |
| 11 | 5MB size limit | Consistent with readImageFile and crush |
| 12 | Build-tag platform gating | Clean unsupported-platform handling; follows crush |
