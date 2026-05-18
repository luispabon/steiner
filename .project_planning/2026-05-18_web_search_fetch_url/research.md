# Web Search Backend Research: Credential-Free Options for Steiner

## Question

What credential-free (no API key, no signup required) web search backends can steiner use to implement the `web_search` tool? The implementation must satisfy the `wonton/web.Searcher` interface (`github.com/deepnoodle-ai/wonton/web`) and return `*web.SearchOutput` with `[]*web.SearchItem` items containing URL, Title, and Description fields. Steiner currently carries stub implementations in `internal/tool/dummy_tools.go`.

---

## Findings

### Option 1: DuckDuckGo HTML Scraping (`html.duckduckgo.com` / `duckduckgo.com/html/`)

**How it works**: DuckDuckGo offers a static, JavaScript-free HTML search page at `https://html.duckduckgo.com/html/?q=<query>` (redirected from `https://duckduckgo.com/html/?q=<query>`). This page renders full organic search results in plain HTML with no dynamic rendering required. Results are contained in `<div class="result">` elements inside a `<div id="links">` container. Each result has a title link, a URL, and a snippet.

**Feasibility**: Moderate. The page is intentionally minimal and scrape-friendly at low volumes. Several Go libraries already do this successfully (see Go Libraries below). However:
- DuckDuckGo returns HTTP 403 on automated requests detected via header anomalies or IP reputation.
- A realistic `User-Agent` (e.g., `Mozilla/5.0 ...`) is required.
- Datacenter IP success rate is ~61% vs ~94% for residential IPs (Oxylabs data).
- HTML structure can change without notice — there is no stability guarantee.
- There is no documented rate limit, but heavy use from a single IP triggers blocks.
- DuckDuckGo's bot detection focuses on: IP reputation, `User-Agent` string, request timing patterns, and missing standard browser headers (e.g., `Accept-Language`, `Accept-Encoding`).

**For an AI agent doing occasional searches** (tens of queries per session, not hundreds per minute), residential or home IPs work reliably. CI/cloud datacenter IPs are riskier.

**Practical rate limit estimate**: No published limit. Anecdotal evidence suggests 30–60 queries/minute before soft-blocking on a single residential IP. A delay of 1–2 seconds between requests is sufficient for agent use.

### Option 2: DuckDuckGo Instant Answer API (`api.duckduckgo.com`)

**Endpoint**: `https://api.duckduckgo.com/?q=<query>&format=json&no_redirect=1&no_html=1`

**Status (May 2026)**: Active and free. No API key, no signup. Confirmed still operational as of March 2026.

**What it returns**: Structured JSON with fields:
- `AbstractText` — Wikipedia-style abstract for the query (often empty for non-entity queries)
- `AbstractURL` — Source URL for the abstract
- `RelatedTopics` — Array of related topic links with `Text` and `FirstURL` fields
- `Results` — Direct answer results (rarely populated for web queries)
- `Type` — `"A"` (article), `"D"` (disambiguation), `"C"` (category), or `""` (none)

**Critical limitation**: This is an **instant answers / knowledge panel API**, not a general web search API. It does not return a list of organic search results. For queries like "golang http server tutorial", `Results` and `RelatedTopics` will typically be sparse or empty. For entity queries like "Eiffel Tower", it returns rich data. It is **not suitable as a primary web search backend** for an AI agent needing general web results.

**Rate limit**: No documented rate limit. The endpoint is marked "use responsibly". Effectively unlimited for low-volume use.

### Option 3: SearXNG Public Instances

**What it is**: SearXNG is a free, open-source metasearch engine that aggregates results from 70+ search engines (Google, Bing, DuckDuckGo, etc.). Public instances are listed at `https://searx.space/`.

**API format**:
```
GET https://<instance>/search?q=<query>&format=json&categories=general
```

Returns JSON:
```json
{
  "results": [
    {
      "url": "https://example.com",
      "title": "Example Title",
      "content": "Snippet text...",
      "engine": "google",
      "score": 1.0
    }
  ],
  "query": "...",
  "number_of_results": 0
}
```

**Critical caveat**: Most public SearXNG instances **disable the JSON format API** (`format=json`) to reduce abuse. Requesting a disabled format returns `403 Forbidden`. Instances that do enable the JSON API often have strict rate limits or become unreliable under load because they act as proxies to real search engines which block high-traffic IPs.

**Self-hosted instance**: Eliminates the format-disabled problem. Can be run via Docker with one command. However, this requires infrastructure beyond steiner's "local-first" model.

**Reliability**: Public instances listed on `searx.space` are often slow (response times 2–10s) and may be down. The `searx.space` site warns that public instances "may yield less accurate results as they have much higher traffic and consequently have a higher chance of being blocked by search providers."

**Go library**: `github.com/morikuni/go-searxng` (last updated March 2025) provides a typed Go client:
```go
client, err := searxng.NewClient(&searxng.ClientOption{URL: "https://instance.example.org"})
output, err := client.Search(ctx, &searxng.SearchInput{Query: "...", Pageno: 1})
// output.Results contains typed GeneralResult, NewsResult, etc.
// GeneralResult has: URL, Title, Content, Engine, Score fields
```
The `GeneralResult` struct maps directly to `web.SearchItem` (URL → URL, Title → Title, Content → Description).

**Rate limit**: Varies by instance. Typically 5–30 requests/minute before soft-blocking on public instances.

### Option 4: Brave Search API

**Status (May 2026)**: Requires signup and API key. Brave eliminated the unconditional free tier for new users in early 2026, replacing it with $5/month in credits (~1,000 queries). Some sources indicate a 2,000 queries/month free plan may still exist for new signups but requires credit card verification and mandatory attribution. **Does not qualify as credential-free.**

### Option 5: Go Libraries for DuckDuckGo HTML Scraping

Several Go libraries exist that scrape `html.duckduckgo.com` without any API key:

| Library | Approach | Last Activity | Notes |
|---|---|---|---|
| `github.com/sap-nocops/duckduckgogo` | Scrapes `html.duckduckgo.com` | Older | Minimal library, HTML scraping, returns URL/Title/Snippet |
| `github.com/kuhahalong/ddgsearch` | DuckDuckGo (specific API unclear) | 2 commits only | Minimal activity, includes CLI tool |
| `github.com/JoshuaDoes/duckduckgolang` | DuckDuckGo Instant Answer API | Older | Wraps `api.duckduckgo.com` — instant answers only, not web results |
| `github.com/ajanicij/goduckgo` | DuckDuckGo API | Older | Similar to above |
| `github.com/Djarvur/ddg-search` | HTML scraping + CLI tool | Active (agent skill) | Designed as a CLI/skill tool for agents, no API key |

**Assessment**: None of these libraries are production-grade maintained packages with semantic versioning and active communities. The most pragmatic approach is to implement the scraper directly in steiner's `internal/tool` package, keeping the dependency surface minimal. The HTML structure of `html.duckduckgo.com` is stable enough for the use case.

### Option 6: SearXNG-as-default with Configurable Endpoint

Allow the user to configure a SearXNG instance URL in steiner's config (defaulting to a known reliable public instance or empty). This makes the tool work for users who self-host SearXNG or know a reliable public instance, while being honest that it requires configuration.

A curated list of SearXNG instances with JSON API enabled (as of May 2026, from `searx.space`) includes instances like `https://search.mdosch.de`, `https://searx.fmac.xyz`, and others — but these change frequently and no single instance can be hardcoded reliably.

---

## Implications

### Recommended approach for steiner

**Primary backend: DuckDuckGo HTML scraping (`html.duckduckgo.com`)**

This is the most viable zero-credential, zero-configuration approach for a local coding agent:

1. Implement a `DuckDuckGoSearcher` struct in `internal/tool/` (or a new `internal/search/` package) that:
   - POSTs or GETs `https://html.duckduckgo.com/html/?q=<encoded_query>`
   - Sets a realistic `User-Agent` and standard browser headers (`Accept`, `Accept-Language`, `Accept-Encoding`)
   - Parses the HTML response using `golang.org/x/net/html` (already a transitive dep via wonton) or a minimal CSS selector library
   - Extracts: result URL (from `<a class="result__a">`), title (text of that anchor), and snippet (text of `<a class="result__snippet">`)
   - Returns `[]*web.SearchItem` mapping to `web.SearchOutput`

2. Respect context cancellation and set a reasonable HTTP timeout (10–15s).

3. Add a configurable `User-Agent` override in steiner config for users who need to customize it.

**Secondary / opt-in: SearXNG via config**

Add an optional `searxng_url` config field. When set, use the SearXNG JSON API instead of DuckDuckGo HTML scraping. This gives power users who self-host SearXNG a higher-quality, more reliable backend. Use `github.com/morikuni/go-searxng` (or implement directly — the HTTP call is trivial) to query it.

**Do not use** the DuckDuckGo Instant Answer API as the primary backend — it does not return general web search results.

**Interface fit**: The `wonton/web.Searcher` interface is already defined at `github.com/deepnoodle-ai/wonton@v0.0.34/web/search.go#L56`. Steiner is on `v0.0.29` — the `Searcher` interface was added in `v0.0.5` so it is available in the current version. The module path in `go.mod` is `github.com/deepnoodle-ai/wonton v0.0.29 // indirect` — it will need to become a direct dependency once the web search tool is implemented.

---

## Risks and Uncertainties

1. **DuckDuckGo HTML structure stability**: The CSS classes on `html.duckduckgo.com` can change without notice. This is the primary ongoing maintenance risk. Mitigate by writing targeted tests against a recorded fixture response.

2. **DuckDuckGo blocking on CI/cloud IPs**: If steiner is run in a cloud environment (GitHub Actions, remote dev boxes), datacenter IP blocks will cause frequent failures. Consider surfacing errors gracefully as "search unavailable" rather than hard failures.

3. **Public SearXNG instance churn**: No public SearXNG instance is reliable enough to hardcode as a default. Do not ship a hardcoded default URL for SearXNG.

4. **DuckDuckGo Instant Answer API deprecation risk**: The API has been available informally for years but is undocumented and has no SLA. It could be removed without notice.

5. **Rate limit uncertainty**: DuckDuckGo does not publish rate limits. Agent loops that search aggressively could trigger IP-level blocks silently (returning degraded results, not errors). Recommend adding inter-request jitter (500ms–2s) in the implementation.

6. **wonton version delta**: Steiner uses `v0.0.29` (indirect); latest is `v0.0.34`. The `Searcher` interface exists in both. Upgrading to make it a direct dep may pull in minor API changes — check the changelog before upgrading.

---

## Sources

- [DuckDuckGo Instant Answer API (Free API Hub)](https://freeapihub.com/apis/duckduckgo-instant-answer-api)
- [DuckDuckGo Instant Answer API (Postman documentation)](https://www.postman.com/api-evangelist/duckduckgo/documentation/i9r819s/duckduckgo-instant-answer-api)
- [DuckDuckGo scraping approach for privacy-first engines (OpsMatters)](https://opsmatters.com/posts/scraping-privacy-first-search-engines-why-duckduckgo-requires-different-approach)
- [How to Scrape DuckDuckGo SERP Data (Bright Data)](https://brightdata.com/blog/web-data/how-to-scrape-duckduckgo)
- [SearXNG Search API documentation](https://docs.searxng.org/dev/search_api.html)
- [SearXNG public instances list](https://searx.space/)
- [SearXNG Wikipedia](https://en.wikipedia.org/wiki/SearXNG)
- [github.com/sap-nocops/duckduckgogo](https://github.com/sap-nocops/duckduckgogo)
- [github.com/JoshuaDoes/duckduckgolang](https://github.com/JoshuaDoes/duckduckgolang)
- [github.com/kuhahalong/ddgsearch](https://github.com/kuhahalong/ddgsearch)
- [github.com/Djarvur/ddg-search](https://github.com/Djarvur/ddg-search)
- [github.com/morikuni/go-searxng (pkg.go.dev)](https://pkg.go.dev/github.com/morikuni/go-searxng)
- [wonton/web package (pkg.go.dev)](https://pkg.go.dev/github.com/deepnoodle-ai/wonton/web)
- [Brave Search API pricing (costbench)](https://costbench.com/software/ai-search-apis/brave-search-api/free-plan/)
- [Brave drops free search API tier (implicator.ai)](https://www.implicator.ai/brave-drops-free-search-api-tier-puts-all-developers-on-metered-billing/)
- [7 Free Web Search APIs for AI Agents (KDnuggets)](https://www.kdnuggets.com/7-free-web-search-apis-for-ai-agents)
- [Top 5 Brave Search API Alternatives in 2026 (Firecrawl)](https://www.firecrawl.dev/blog/brave-search-api-alternatives)

---

## Open Questions

1. **HTML parser dependency**: Should the DuckDuckGo scraper use `golang.org/x/net/html` (transitive via wonton) or add a lightweight CSS-selector library like `github.com/andybalholm/cascadia`? Using `x/net/html` directly keeps deps minimal but requires more boilerplate; `cascadia` or `github.com/PuerkitoBio/goquery` would be more ergonomic.

2. **Should `web_search` be in `internal/tool/` or a new `internal/search/` package?** The Searcher interface is defined in wonton/web, which is already a transitive dep. A dedicated `internal/search/` package with a `DuckDuckGoSearcher` struct and optional `SearXNGSearcher` would be cleaner and more testable.

3. **Config schema for search backend**: What config key should control the search backend? Suggestion: `search.backend` (values: `"duckduckgo"` default, `"searxng"`) and `search.searxng_url` for the instance URL.

4. **Graceful degradation**: Should the tool return a soft error (returning an empty result set with a message) or a hard error when DuckDuckGo blocks the request? Hard errors interrupt the agent loop; soft errors may cause the model to silently proceed without search results.

5. **Fixture testing**: Can a recorded HTTP response from `html.duckduckgo.com` be stored in `testdata/` and used for unit tests without network access? This is strongly recommended to make tests deterministic and avoid rate limit issues in CI.

6. **wonton version upgrade**: Should `wonton` be bumped from `v0.0.29` to `v0.0.34` as part of this work? The Searcher interface exists in both. Upgrading is low-risk but should be a deliberate decision.
