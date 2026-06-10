# Image Paste Support (Deferred)

Deferred from the image support plan (2026-06-10_image_support). This document captures findings and scope for a future planner to pick up clipboard image paste.

## Context

The initial image support plan covers:
- Image content block types in agent/provider message pipeline
- `read` tool extension for image files on disk
- Vision capability detection + warnings
- TUI placeholder display for images

Clipboard image paste (Ctrl+V) was deferred because Bubble Tea does not natively support image paste, requiring platform-specific external tool dependencies.

## Findings

### Bubble Tea Limitation

Bubble Tea v1.3.x `PasteMsg` carries text content only (string type). No binary or image data handling exists in bubbletea's paste event system. The `atotto/clipboard` Go package is also text-only (Read/WriteAll for strings).

### Required Workarounds

Image data must be read from the OS clipboard via shell commands:

| Platform | Command | Notes |
|----------|---------|-------|
| Linux/X11 | `xclip -selection clipboard -t image/png -o` | Requires `xclip` installed, `DISPLAY` set |
| Wayland | `wl-paste -t image/png` | Requires `wl-clipboard` installed |
| macOS | `pbpaste -Prefer png` | Built-in, no extra deps |

### Implementation Approach

1. **Detect Ctrl+V in TUI**: Intercept paste keypress before textarea handles it
2. **Probe clipboard content type**: Run OS tool to check if clipboard contains image data vs text
3. **Extract image data**: Shell out to appropriate OS tool, capture binary stdout
4. **Encode and attach**: Base64-encode the image data, attach as `ImageBlock` to the pending user message
5. **Fallback**: If no image in clipboard or tool missing, let textarea handle paste normally (text)

### Platform Detection

```go
// Detect clipboard tool
func detectClipboardTool() (name string, args []string, ok bool) {
    // Check macOS first
    if runtime.GOOS == "darwin" {
        return "pbpaste", []string{"-Prefer", "png"}, true
    }
    // Check Wayland
    if os.Getenv("WAYLAND_DISPLAY") != "" {
        if _, err := exec.LookPath("wl-paste"); err == nil {
            return "wl-paste", []string{"-t", "image/png"}, true
        }
    }
    // Check X11
    if os.Getenv("DISPLAY") != "" {
        if _, err := exec.LookPath("xclip"); err == nil {
            return "xclip", []string{"-selection", "clipboard", "-t", "image/png", "-o"}, true
        }
    }
    return "", nil, false
}
```

### Risks

- **External dependencies**: `xclip` / `wl-clipboard` may not be installed on Linux; need graceful fallback with informative error
- **Platform detection edge cases**: Wayland sessions with X11 fallback, remote terminals, WSL
- **Clipboard probe latency**: Checking clipboard content type on every Ctrl+V adds latency; may need caching or async probe
- **Large images**: Clipboard may contain very large screenshots; need size validation before encoding
- **Format negotiation**: Clipboard may offer multiple formats (PNG, JPEG, BMP); need to prefer PNG for lossless

### Prerequisites

This work depends on the content block infrastructure from the initial image support plan:
- `ImageBlock` type in `agent.Message` and `provider.Message`
- Anthropic and OpenAI wire format support for image blocks
- TUI image placeholder rendering
- Vision capability detection

### Scope

- **In**: Ctrl+V image paste detection, OS clipboard image extraction, attach to user message, platform detection, graceful fallback when tools missing
- **Out**: Drag-and-drop (terminal limitation), remote URL paste, image editing, multiple image paste in single action
