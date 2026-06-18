## Request

Batch three small GitHub issues into a single piece of work:

- **#221** — fetch_url: handle other media types
- **#220** — Change sidebar model info to show provider instead of host
- **#219** — Prompt box 12-line newline limit

## Overview

Three independent, low-risk changes touching separate packages.

**#221 — fetch_url: broaden text-like content types.** The `isTextLikeContentType` guard in `internal/tool/builtin/fetch_url_image.go` uses an exhaustive allowlist. URLs returning `text/csv`, `application/javascript`, `application/yaml`, `application/ld+json`, or any `text/*` variant not in the list get rejected with "unsupported content type". Fix: accept all `text/*` via prefix match and add common application text types.

**#220 — Sidebar: show provider name instead of host.** `sidebar_sections.go:61-65` renders a "host" card field from `stripProviderURL(s.provider)`, which strips the URL scheme/suffix. Instead, show the provider config key name (e.g. "anthropic", "ollama", "openrouter") with label "provider". Requires threading a `providerName` field through `tui.Config` → `sidebarState` → `modelSection`, plus a `modelProviderNames` map (model name → provider name) parallel to `modelBaseURLs`.

**#219 — Textarea MaxHeight bump.** `model_init.go:73` sets `input.MaxHeight = 10`. ALT+ENTER stops inserting newlines once the textarea hits this cap. Bump to 30 (user chose fixed value over dynamic).

## Key Decisions

1. **#221 — prefix match for text/***: Accept any `text/*` content type, plus explicit `application/` text types (`application/javascript`, `application/typescript`, `application/yaml`, `application/x-yaml`, `application/ld+json`, `application/graphql`, `application/x-www-form-urlencoded`). Binary `application/*` types still rejected.
2. **#220 — provider name from config key**: Use the provider config map key (e.g. "anthropic") rather than trying to derive a friendly name from the URL or type enum. This is what users configure and recognise.
3. **#219 — fixed MaxHeight = 30**: Simple bump. No dynamic sizing based on terminal height.

## Tradeoffs

- **#221**: Could have removed the content-type gate entirely and let wonton/fetch handle everything non-image. Kept the gate because binary types (video, audio, pdf) would waste context or crash the fetcher.
- **#220**: Could have shown `ProviderType` enum value (e.g. "openai_compat"). Chose config key name because it's user-defined, more recognisable, and avoids exposing internal type names.
- **#219**: Dynamic MaxHeight (percentage of terminal) adapts better to small terminals but adds resize-handling complexity for marginal benefit.

## Scope Boundaries

**In scope:**
- `isTextLikeContentType` broadening + tests
- Sidebar label rename + provider name plumbing + tests
- Textarea MaxHeight bump + test update if existing test asserts the value
- Doc updates per CLAUDE.md maintenance rules (none triggered — no tool add/remove, no config field visible to users, no delegation change)

**Out of scope:**
- fetch_url architecture changes (streaming, retry, etc.)
- Sidebar layout redesign
- Dynamic textarea sizing
- Any other "small" labelled issues that may appear later

## Verification Strategy

- `gofmt -w <files>` and `goimports -w <files>` after edits
- `go test ./internal/tool/builtin/... -run TestFetchURL` for #221
- `go test ./internal/tui/... -run TestSidebar` for #220
- `go test ./internal/tui/... -run TestModel` for #219
- `go vet ./...` for static checks
- `make check` as final gate (runs fmt-check, imports-check, tidy-check, vet, lint, vuln, test-race, build)

Commands: `make check` (expensive but comprehensive), `go test ./internal/tool/builtin/... ./internal/tui/...` (cheap, targeted).

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | Batch all three into one branch | All small, independent, low-risk |
| 2 | text/* prefix match | Covers all text variants without enumeration |
| 3 | Provider config key as display name | User-recognisable, already available in config |
| 4 | MaxHeight = 30 fixed | User preference, simple |
