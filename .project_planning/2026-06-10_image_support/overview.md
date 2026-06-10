## Request

Support image input in steiner prompts (GitHub issue #93). Enable multimodal workflows with vision-capable models by:
1. Extending the `read` tool to detect and return image files as structured content blocks
2. Adding image content block types through the full message pipeline (agent → provider → wire)
3. Detecting model vision capability and warning when images are sent to blind models
4. Displaying image placeholders in the TUI conversation view

Clipboard image paste is deferred to a follow-up (see `.project_planning/image_paste.md`).

## Overview

The current message pipeline is text-only: `agent.Message.Content` and `provider.Message.Content` are plain strings. Tool results collapse to strings via `ToolResultEnvelope`. This plan introduces structured content blocks alongside the existing string content, threading image data from tool execution through provider wire formats to the model.

### Content Block Model

Add an `ImageBlock` type to both `agent` and `provider` packages, carrying base64-encoded image data with media type. Messages gain an `Images []ImageBlock` field. The existing `Content` string field remains the primary text channel — no migration needed.

### Read Tool Extension

When `read` encounters an image file (detected by extension: `.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`), it reads the file, base64-encodes it, and returns a result containing the image block. The text content returned to the model is a metadata summary (`[image: 1024x768 png 234KB]`), while the image data travels via a new sideband field on the result struct.

### Provider Wire Format

- **Anthropic**: User messages with images emit `{"type": "image", "source": {"type": "base64", "media_type": "...", "data": "..."}}` content blocks alongside text blocks
- **OpenAI**: User messages with images emit `{"type": "image_url", "image_url": {"url": "data:image/...;base64,...", "detail": "auto"}}` content blocks

Tool result messages carrying images are converted to user messages with both `tool_result` and `image` blocks (Anthropic) or appended as follow-up user messages (OpenAI).

### Vision Capability

A `Vision *bool` field on `ModelConfig` lets operators declare whether a model supports images. When unset, steiner assumes vision is supported (most modern models are vision-capable). When explicitly `false`, image blocks are stripped from requests and a diagnostic warning is emitted.

### TUI Display

Images in conversation messages render as placeholder text: `[image: 1024x768 png 234KB]` styled distinctly from regular text.

### Compaction

During compaction, image blocks are dropped and replaced with a text marker `[image was attached]` to save tokens.

## Key Decisions

1. **Images as sideband field, not replacing Content**: `Message.Images []ImageBlock` alongside `Content string`. Avoids migrating every message consumer to a content-block array model. Text stays simple.
2. **Extension-based image detection in read**: Using file extension rather than content sniffing. Simple, reliable, no magic bytes parsing needed. The existing `isBinary` check already prevents binary files from being read as text.
3. **Config flag for vision, not hardcoded model lists**: `vision: true/false` in `ModelConfig` avoids rotting allowlists. Defaults to assumed-capable (omitted = vision supported).
4. **Tool result image sideband**: New `ImageBlock` field on `ToolResultEnvelope` carries image data alongside the text summary. The agent loop attaches it to the tool result message.
5. **Drop images on compaction**: Images are expensive (1000+ tokens each). Compaction replaces them with `[image was attached]` text marker.

## Tradeoffs

1. **Sideband field vs full content-block array**: A full content-block model (like Anthropic's native format) would be more flexible but requires rewriting every message consumer. The sideband approach is surgical — only image-aware code paths need changes.
2. **Default vision=true vs vision=false**: Defaulting to true means images flow to models that might not support them, potentially causing API errors. But most modern models support vision, and explicit errors are more informative than silent stripping. Users of older models can set `vision: false`.
3. **Extension-based vs content-sniffing**: Extension-based detection misses extensionless image files but avoids false positives on binary files that happen to have image-like magic bytes.

## Scope Boundaries

**In scope:**
- `ImageBlock` type in agent and provider packages
- `read` tool image file detection and base64 encoding
- Anthropic wire format image content blocks
- OpenAI wire format image_url content blocks
- `Vision` config field on `ModelConfig`
- Vision capability check with diagnostic warning
- TUI image placeholder rendering
- Compaction image dropping
- Image size validation (reject files > 5MB)
- Unit tests for all new code paths

**Out of scope:**
- Clipboard image paste (deferred, see `.project_planning/image_paste.md`)
- Image generation/output from models
- Remote URL image fetching
- Sub-agent image forwarding
- Image caching or optimization
- Multiple image support per user message (single image per read call is fine; pipeline supports multiple)
- Token cost estimation for images

## Verification Strategy

### Commands

| Command | Purpose | Cost |
|---------|---------|------|
| `gofmt -w <files>` | Format edited files | cheap |
| `goimports -w <files>` | Fix imports | cheap |
| `go vet ./...` | Static analysis | cheap |
| `go test ./path/to/pkg -run TestName` | Targeted tests | cheap |
| `go test ./...` | Full test suite | medium |
| `go build ./...` | Build check | medium |
| `golangci-lint run ./...` | Lint | medium |
| `make check` | Full verification (required before finalization) | expensive |

### Strategy

- Run targeted tests after each step
- Run `go build ./...` after wire format changes to catch type errors early
- Run `make check` at the end
- Prefer `gofmt -w` + `goimports -w` after every Go edit

## Decision Log

| Date | Decision | Rationale |
|------|----------|-----------|
| 2026-06-10 | Defer clipboard paste | Bubble Tea lacks image paste support; OS tool dependencies add complexity. Ship file-based path first. |
| 2026-06-10 | Extend `read` tool (not separate tool) | Consistent UX, one tool handles all file reading. Image detection by extension. |
| 2026-06-10 | Config flag for vision capability | No API metadata available. Hardcoded lists rot. Config flag is maintainable. |
| 2026-06-10 | Sideband `Images` field (not content-block array) | Surgical change, avoids full message model migration. |
| 2026-06-10 | Drop images on compaction | Images are expensive tokens. Text marker preserves context that an image existed. |
| 2026-06-10 | Image file input via `read` tool (model-initiated) | User preferred model calling tool with file path over user `/attach` command. |
