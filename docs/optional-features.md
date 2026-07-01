# Optional Features

## cave_human

`cave_human` combines terse-output behavior with an anti-AI-writing style instruction. It keeps responses short and direct while avoiding filler, hedging, and common AI-writing tells. The instruction is injected into the system preamble, compaction prompts, and sub-agent prompts so it stays consistent throughout a session.

When enabled, compaction summaries switch to a denser, purpose-built encoding style instead of reusing the chat-facing voice: articles are dropped where meaning survives, facts use `key=value` shorthand, semicolons replace sentences, and markdown headers are skipped in favor of `label:` prefixes — maximizing information density per token in the handoff summary.

Disabled by default. Enable via config file only:

```yaml
# config.yaml
cave_human: true
```

## Accent colour

The TUI accent colour can be changed with `/accent`. With no argument it opens a colour picker showing all 13 presets with colour swatches. With a preset name it sets the colour directly:

```
/accent          # open picker
/accent violet   # set directly
```

**Available presets:** `amber` (default), `coral`, `rose`, `magenta`, `gold`, `violet`, `indigo`, `blue`, `cyan`, `teal`, `green`, `mint`, `lime`.

Use `random` to pick a different preset on each startup:

```
/accent random
```

The selected preset is saved to `~/.config/steiner/prefs.yaml` and restored on next launch. With `random`, the preference is kept as `random` so a new colour is chosen each time.

## Web search

The `web_search` tool lets the model search the web and return URL, title, and description results. It is **disabled by default** — it only appears in the tool registry when `search.backend` is set in config.

| Backend | Config key | Auth |
|---------|------------|------|
| Google | `google` | `GOOGLE_SEARCH_CX` + `GOOGLE_SEARCH_API_KEY` |
| Kagi | `kagi` | `KAGI_API_KEY` |
| Brave | `brave` | `BRAVE_API_KEY` |
| SearXNG | `searxng` | `search.searxng_url` (self-hosted, no API key) |

Enable by adding a `search` block to your config:

```yaml
search:
  backend: brave   # one of: google, kagi, brave, searxng
```

When enabled, `web_search` is also added to the `research` sub-agent's tool allowlist automatically.

## Image paste

Paste images directly in the interactive TUI with **Ctrl+V**. Images are read from the clipboard or referenced by file path, resized to a max of 2048px on the longest side, and token-accounted automatically. After the model responds, image data is stripped from the conversation and replaced with a text placeholder, keeping context lean. Models without vision capability have images stripped before sending.

Supported formats: PNG, JPG, JPEG, GIF, WebP. Max size: 5MB.

## Conversation forking

Fork the current conversation or any saved session into a new independent session. The new session carries the full conversation history from the source, allowing you to explore alternative directions without modifying the original.

**In interactive mode**, use `/fork` to fork the current live session:

```
/fork
```

**In the session picker**, press `f` on any saved session to fork it. The new session is created with the title "Fork of: <original title>" and can be resumed, edited, or deleted like any other session.

Forks are independent — changes in one session do not affect the source.

## Code simplification

Analyze branch changes for structural and code quality improvements before review. `/simplify` dispatches four parallel `explore` sub-agents — one each for reuse, simplification, efficiency, and altitude — against the changed files on your current branch, refines findings through an advisor sanity check, and presents a consolidated report. On confirmation, it enters a fix/review loop to apply approved improvements.

```
/simplify          # analyze against main (default)
/simplify develop  # analyze against a custom base branch
```

**Categories analyzed:**

- **Reuse** — duplicated logic, extractable helpers, dead code, unused existing utilities
- **Simplification** — naming clarity, unnecessary nesting, verbose constructs, excess abstraction
- **Efficiency** — unnecessary allocations, wasteful iteration, hot-path redundancy, unnecessary I/O
- **Altitude** — logic at the wrong abstraction level, mixed concerns, package boundary violations

`/simplify` does not hunt for bugs — use `/review` for that. All proposed changes preserve existing behavior.

## Codex OAuth

Use your OpenAI Codex subscription (GPT-5.5, GPT-5.4, etc.) with Steiner without a separate API key. Codex providers use the Responses wire format. When login can exchange the ChatGPT ID token for an API-key style credential, Steiner sends requests to `https://api.openai.com/v1/responses`; otherwise it uses the ChatGPT Codex backend at `https://chatgpt.com/backend-api/codex/responses` with the saved OAuth access token and account metadata.

**Setup:**

1. Authenticate with your OpenAI account:
   ```bash
   steiner login codex
   ```
   This opens a browser for OAuth consent and saves a token to `~/.config/steiner/codex_auth.json`.

2. Check authentication status:
   ```bash
   steiner login codex status
   ```

3. Configure a provider and model:
   ```yaml
   providers:
     codex:
       type: codex

   models:
     gpt-5:
       provider: codex
       id: gpt-5.5
   ```

Token storage at `~/.config/steiner/codex_auth.json` is created with `0600` permissions and should be treated as sensitive — the same as API keys and `--log-file` output. Existing token files continue to load, but re-running `steiner login codex` refreshes stored `id_token`, ChatGPT account metadata, and the optional exchanged API credential used for direct OpenAI Responses API calls.
