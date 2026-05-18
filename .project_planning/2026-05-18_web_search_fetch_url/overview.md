## Request

Implement real `web_search` and `fetch_url` tools to replace the dummy stubs in `internal/tool/dummy_tools.go`. Search backend must be pluggable (Google, Kagi, Brave, SearXNG) and configured via steiner config. When no search backend is configured, `web_search` is not registered and the `research` agent type is disabled. `fetch_url` is always available (no credentials needed).

## Overview

### fetch_url

Wrap dive's `toolkit.FetchTool` in a steiner `ToolDef`, following the same pattern as `builtin/bash.go`. Dive's implementation handles HTTP fetching, HTML-to-markdown conversion, SSRF protection, content truncation, and retry logic. No credentials needed — always registered.

**Schema**: `url` (required string), `max_size` (optional int, default 500k runes).

**Result shape**: structured JSON with `title`, `description`, `content` (markdown), `content_length`, and `url` fields.

**Approval mode**: auto (read-only network access, SSRF-protected).

### web_search

Pluggable search tool backed by a configurable `web.Searcher` implementation. Only registered when a search backend is configured.

**Backends**:
- `google` — Google Custom Search API. Requires `GOOGLE_SEARCH_CX` and `GOOGLE_SEARCH_API_KEY` env vars. Uses dive's `experimental/toolkit/google` package.
- `kagi` — Kagi Search API. Requires `KAGI_API_KEY` env var. Uses dive's `experimental/toolkit/kagi` package.
- `brave` — Brave Search API. Requires `BRAVE_API_KEY` env var. Custom `web.Searcher` implementation in steiner (dive has no Brave support). Simple HTTP GET + JSON parse against `https://api.search.brave.com/res/v1/web/search`.
- `searxng` — SearXNG instance. Requires `search.searxng_url` config field. No API key needed if self-hosted.

**Schema**: `query` (required string), `limit` (optional int, default 10, max 30).

**Result shape**: structured JSON array of `{url, title, description}` items.

**Approval mode**: auto.

### Config

Add `SearchConfig` to `config.Config`:

```yaml
search:
  backend: ""          # "google" | "kagi" | "brave" | "searxng" | "" (disabled, default)
  searxng_url: ""      # required when backend is "searxng"
```

- `backend: ""` (default) — `web_search` not registered, research agent disabled
- Google and Kagi read API keys from env vars (consistent with dive's existing patterns)
- SearXNG reads instance URL from config

### Package placement

- `internal/tool/builtin/web_search.go` — steiner ToolDef wrapping any `web.Searcher` backend
- `internal/tool/builtin/fetch_url.go` — steiner ToolDef wrapping dive's FetchTool
- `internal/tool/builtin/search_backend.go` — factory that builds a `web.Searcher` from config, returns nil when no backend configured
- `internal/tool/builtin/brave_search.go` — Brave Search `web.Searcher` implementation (HTTP client, JSON response mapping)

### Tool availability

**fetch_url**: always registered in `Builtins()` for parent model. Always in `extendedBase` for sub-agents.

**web_search**: conditionally registered — only when `SearchConfig.Backend` is set and a `web.Searcher` can be constructed.

**Parent model**: both tools registered (web_search only when backend configured).

**Sub-agents**:
- `research` — has `web_search` + `fetch_url` in allowlist. **Disabled entirely when no search backend is configured** (its primary value is web research; without web_search it's just a weaker explore agent).
- `explore` — no web tools
- `code` — no web tools
- `plan` — no web tools
- `verify` — no web tools

### Conditional research agent

The research agent type must be excluded from `AllAgentTypes()` and from the specialized delegate tool registration when `web_search` is unavailable. This means the agent type availability becomes config-dependent.

Approach: `AllAgentTypes()` stays static. The filtering happens at registration time in `buildActiveRegistry` — skip the research specialized tool def when no search backend is configured. The research type's allowlist references `web_search`, which won't exist in the registry, so even if somehow invoked it would have no web tools.

### Wiring changes

- Add `SearchConfig` to `config.Config`
- Add `NewFetchURLTool(env)` to `Builtins()` in `builtin/builtins.go` (always)
- `NewWebSearchTool` takes a `web.Searcher` — only called and registered when backend is configured
- `NewSearchBackend(cfg config.SearchConfig) (web.Searcher, error)` factory in `builtin/search_backend.go`
- In `buildActiveRegistry`: conditionally register `web_search` in both base and extendedBase; skip research agent specialized tool when no backend
- Remove `DummyWebSearchTool()` and `DummyFetchURLTool()` from `internal/tool/dummy_tools.go`
- Remove `dummy_tools_test.go`
- Remove old dummy registration from `buildActiveRegistry` in `cmd/steiner/runner.go`
- Promote `wonton` from indirect to direct dependency in `go.mod`

### Error handling

- Search backend misconfigured (e.g. `backend: "google"` but env vars missing): fail at startup with clear error message
- fetch_url network failure: structured error result `{"ok": false, "error": "..."}`, agent loop continues
- web_search network failure: structured error result, agent loop continues
- Invalid input (empty query, invalid URL): return validation error

### Testing

- `search_backend.go`: unit tests for factory — correct backend for each config value, nil when empty, error on misconfiguration
- `brave_search.go`: unit tests with `httptest.NewServer` mocking Brave API responses
- `web_search.go`: unit tests with mock `web.Searcher` — verifies ToolDef handler shapes results correctly
- `fetch_url.go`: unit tests with `httptest.NewServer` — verifies steiner wrapper around dive's FetchTool
- Config: test that `SearchConfig` deserializes correctly from YAML
- Delegation: test that research agent is excluded when no search backend
- Integration: manual verification with real backends

### Not in scope

- DuckDuckGo scraping (violates robots.txt)
- Rate limiting / inter-request jitter
- User-Agent config override
- Additional search backends beyond Google, Kagi, Brave, SearXNG

## Verification Strategy

### Sources
- CLAUDE.md (project instructions)
- Makefile (`check` target)
- `.github/workflows/checks.yml`

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### formatting
- preferred_mode: fix
- fix:
  - `gofmt -w <changed_files>`
  - `goimports -w <changed_files>`
- check:
  - `make fmt-check`
  - `make imports-check`
- use_check_only_when:
  - CI or reviewer final pass

#### build
- preferred_mode: check
- fix:
  - n/a
- check:
  - `go build ./...`
  - `make build-binaries`
- use_check_only_when:
  - always check-only

#### unit-tests
- preferred_mode: check
- fix:
  - n/a
- check:
  - `go test ./internal/tool/builtin/... -run <TestName>`
  - `go test ./internal/tool/... -run <TestName>`
  - `go test ./cmd/steiner/... -run <TestName>`
  - `go test ./internal/config/... -run <TestName>`
  - `go test ./...`
- use_check_only_when:
  - always check-only

#### race-tests
- preferred_mode: check
- fix:
  - n/a
- check:
  - `go test -race ./...`
- use_check_only_when:
  - always check-only

#### vet
- preferred_mode: check
- fix:
  - n/a
- check:
  - `go vet ./...`
- use_check_only_when:
  - always check-only

#### lint
- preferred_mode: check
- fix:
  - n/a
- check:
  - `golangci-lint run ./...`
- use_check_only_when:
  - always check-only

#### tidy
- preferred_mode: fix
- fix:
  - `go mod tidy`
- check:
  - `make tidy-check`
- use_check_only_when:
  - CI or final pass

#### vuln
- preferred_mode: check
- fix:
  - n/a
- check:
  - `govulncheck ./...`
- use_check_only_when:
  - always check-only

### Tiers
- cheap:
  - formatting
  - vet
  - build
- medium:
  - unit-tests
  - tidy
  - lint
- expensive:
  - race-tests
  - vuln

### Required Boundaries
- step_level_exceptions:
  - none
- stage_level_exceptions:
  - none
- end_of_implementation:
  - formatting
  - build
  - unit-tests
  - race-tests
  - vet
  - lint
  - tidy
  - vuln
- reviewer_after_fix:
  - rerun failed checks plus targeted unit tests

### Assumptions
- `golangci-lint`, `goimports`, `govulncheck` are installed (per `make install-check-tools`)
- `make check` runs all verification in correct order
- Dive's `experimental/toolkit/google` and `experimental/toolkit/kagi` packages are stable enough to use (they're in `experimental/` but have tests)

### Uncertainties
- Whether dive's google/kagi packages need any version bump or have breaking changes between wonton v0.0.29 and current

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | Pluggable search backend (Google, Kagi, Brave, SearXNG) | DuckDuckGo scraping violates robots.txt. All viable search APIs require credentials. Make it configurable. |
| 2 | No search backend = tool disabled + research agent disabled | Tool shouldn't exist if it can't work. Research agent's value is web access — without it, it's a weaker explore agent. |
| 3 | fetch_url always available | No credentials needed. Dive's FetchTool has SSRF protection built in. |
| 4 | Config-driven backend selection | `search.backend` field in steiner config. Clean, explicit, no magic env var detection. |
| 5 | Google/Kagi use env vars for API keys | Consistent with dive's existing patterns. Avoids secrets in config files. |
| 6 | SearXNG uses config URL field | Instance URL is not a secret. Config is the right place. |
| 7 | Fail at startup on misconfigured backend | Better than silent failure at search time. User gets immediate feedback. |
| 8 | Tools in `internal/tool/builtin/` | Follows existing pattern (bash, read, grep, etc.). |
| 9 | Remove dummy_tools.go entirely | Real implementations and conditional registration replace stubs. |
| 10 | wonton promoted to direct dep | `web.Searcher` interface used directly in implementation. |
| 11 | DuckDuckGo scraping rejected | robots.txt explicitly disallows `/html`, `/lite`, and `/*?`. |
| 12 | Research agent filtering at registration time | `AllAgentTypes()` stays static. `buildActiveRegistry` skips research specialized tool when no backend. |
| 13 | Brave Search as custom `web.Searcher` | Dive/wonton have no Brave support. Simple HTTP+JSON implementation (~60 lines). Popular API for AI agents, worth including. |
