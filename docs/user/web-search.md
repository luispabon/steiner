# Web search

`web_search` lets the model search the web. It returns a URL, title and description for each result. The tool is off until you configure `search.backend`, and Steiner registers it only while sub-agent delegation is enabled (the default). With a backend configured, the `research` sub-agent also gets the tool; without one, that sub-agent type is not offered.

Each call takes a query and an optional `limit` (default 10, capped at 30). Only the three fields above enter the conversation, so pages are not injected wholesale; use `fetch_url` on a result URL when you need the content.

This page covers the four backends, their setup, a local SearXNG example, and checks for when search does not work. Field reference: [`search` block](configuration.md#search-block).

## Prerequisites

- Sub-agent delegation enabled. `sub_agent.enabled` defaults to `true`; when it is `false`, `web_search` is not registered at all, even with a backend configured.
- Exactly one backend selected in `search.backend`.
- The credentials that backend needs, in config or in the environment.

The unprefixed environment variables below fill a config field only when that field is empty, so config wins over the environment. The backend and SearXNG URL have no environment override. Config validation fails when the selected backend is missing a required field. Restart Steiner after changing the config or its environment.

## Choose a backend

| `search.backend` | Credentials | Notes |
|------------------|-------------|-------|
| `brave` | `search.brave_api_key` or `BRAVE_API_KEY` | Official API; see [pricing](https://api-dashboard.search.brave.com/documentation/pricing). |
| `google` | `search.google_cx` and `search.google_api_key`, or `GOOGLE_SEARCH_CX` and `GOOGLE_SEARCH_API_KEY` | Custom Search JSON API. Closed to new customers; existing customers only. |
| `kagi` | `search.kagi_api_key` or `KAGI_API_KEY` | Paid API, billed separately from a Kagi subscription. |
| `searxng` | `search.searxng_url` | Self-hosted metasearch. No API key. |

## Brave Search API

Brave offers a search API.

1. Create an account and API key at [api-dashboard.search.brave.com/register](https://api-dashboard.search.brave.com/register).
2. Create a key in the dashboard and copy it.
3. Read the [Web Search getting started guide](https://api-dashboard.search.brave.com/app/documentation/web-search/get-started) and the [pricing page](https://api-dashboard.search.brave.com/documentation/pricing) for current terms and limits.

Configure the backend and export the key:

```yaml
search:
  backend: brave
```

```bash
# ~/.bashrc or your shell's equivalent, or set brave_api_key in config
export BRAVE_API_KEY=your-key
```

Steiner sends the key as `X-Subscription-Token` to `https://api.search.brave.com/res/v1/web/search` and caps the returned count at 20.

## Google Custom Search JSON API

**Availability:** Google has closed the Custom Search JSON API to new customers. Existing customers can keep using it until January 1, 2027, when it is discontinued. Verify the current state on the [official overview](https://developers.google.com/custom-search/v1/overview) before starting; if you cannot sign up, use Brave, Kagi or SearXNG instead.

For existing customers, setup takes three parts:

1. Create a Programmable Search Engine at [programmablesearchengine.google.com/controlpanel/create](https://programmablesearchengine.google.com/controlpanel/create). For general web results the engine must search the entire web, not just the sites you list. Copy the Search engine ID (the `cx` value) from the engine's Basic settings.
2. Enable the Custom Search API for a Google Cloud project: [console.cloud.google.com/apis/library/customsearch.googleapis.com](https://console.cloud.google.com/apis/library/customsearch.googleapis.com).
3. Create an API key under [Credentials](https://console.cloud.google.com/apis/credentials). Restrict the key to the Custom Search API if you can.

```yaml
search:
  backend: google
```

```bash
export GOOGLE_SEARCH_CX=your-search-engine-id
export GOOGLE_SEARCH_API_KEY=your-api-key
```

See Google's [overview](https://developers.google.com/custom-search/v1/overview) for current pricing and usage limits. Steiner requests one page per 10 results, so `limit: 10` needs one page and `limit: 30` needs three. Failed requests may be retried.

## Kagi Search API

Kagi requires an account, and API usage is billed separately from a Kagi subscription.

1. Create a Kagi account.
2. Open the API portal at [kagi.com/api](https://kagi.com/api), or Settings then API. Add a payment method and copy the generated key. The [Quick Start](https://help.kagi.com/kagi/api/quick-start.html) walks through the same steps.
3. Check the key in the portal playground if you want to test it before configuring Steiner.

```yaml
search:
  backend: kagi
```

```bash
export KAGI_API_KEY=your-key
```

Steiner's Kagi backend calls the legacy v0 endpoint, `https://kagi.com/api/v0/search`, with an `Authorization: Bot <key>` header. Kagi marks v0 as deprecated in favour of v1 (see [Search API (Legacy)](https://help.kagi.com/kagi/api/search-legacy.html)). Keys are issued from the same API portal, so no separate credential is needed, but if Kagi retires v0 the backend will need updating. See the legacy page for current pricing and usage terms.

## SearXNG

SearXNG is a self-hosted metasearch front end: it fans a query out to other search engines and aggregates the results. It needs no API key, but its results depend on the engines it queries, and many public instances disable the JSON format that Steiner needs.

Requirements for a usable instance:

- The JSON format is enabled in `settings.yml` under `search.formats`. Without it, requests with `format=json` return 403.
- The instance is reachable from the machine running Steiner.
- `search.searxng_url` is the base URL without a trailing slash. Steiner appends `/search`, sends `format=json`, `categories=general` and `count`, and expects a response within 15 seconds.

### Local instance with Docker Compose

This example runs a private instance on `http://localhost:8888`, reachable only from the local machine. It is a starting point, not a production deployment. For public or shared instances, use the upstream [container installation guide](https://docs.searxng.org/admin/installation-docker.html), which covers the full Compose template, Valkey for the limiter, and reverse proxy and TLS setup.

Run these commands in Bash, not fish, to create the directory and generate a secret. The command refuses to overwrite an existing `.env` file:

```bash
mkdir -p searxng-local/searxng
cd searxng-local
(umask 077; set -C; printf 'SEARXNG_SECRET=%s\n' "$(openssl rand -hex 32)" > .env)
```

`compose.yaml`:

```yaml
services:
  searxng:
    image: docker.io/searxng/searxng:latest
    ports:
      - "127.0.0.1:8888:8080"
    volumes:
      - ./searxng:/etc/searxng
    env_file: .env
    environment:
      SEARXNG_BASE_URL: http://localhost:8888/
    restart: unless-stopped
```

`searxng/settings.yml`:

```yaml
use_default_settings: true

server:
  limiter: false

search:
  formats:
    - html
    - json
```

Notes on this setup:

- The secret is read from `SEARXNG_SECRET` in `.env`, not from `settings.yml`. Keep `.env` out of version control and do not share it. SearXNG refuses to start in production with its default secret.
- `json` in `search.formats` is what allows the Search API to return JSON. The default format list contains only `html`.
- The limiter is off because this example runs no Valkey and the port is bound to loopback. Do not expose the container beyond `127.0.0.1` with these settings.
- The example binds the host port 8888 to the container's port 8080. Change the host side if 8888 is taken, and keep `SEARXNG_BASE_URL` and `search.searxng_url` in step with it.

Start it and check the JSON endpoint:

```bash
docker compose up -d
curl -sS 'http://localhost:8888/search?q=steiner&format=json' | head -c 300
```

A JSON body containing a `results` array shows the JSON endpoint is responding. This preview does not show the HTTP status; check the full response if it is truncated.

Configure Steiner:

```yaml
search:
  backend: searxng
  searxng_url: http://localhost:8888
```

### Public instances

Many public SearXNG instances disable JSON, and some block automated clients, so a public instance may not be usable for `web_search`. A 403 can mean JSON is disabled, but can also have other causes, such as access controls or blocking automated clients. Self-hosting lets you configure the instance, but queries still leave your machine and are sent to upstream search engines.

## Verify

1. Validate the config without printing its secrets: `steiner config > /dev/null && echo ok`. This also validates the project config; see [Configuration](configuration.md#file-locations-and-precedence).
2. Check the provider directly with one of these. Replace the SearXNG URL if you changed the port.

```bash
# Brave
curl -sS -o /dev/null -w '%{http_code}\n' \
  -H "X-Subscription-Token: $BRAVE_API_KEY" \
  'https://api.search.brave.com/res/v1/web/search?q=steiner'

# Google
curl -sS -o /dev/null -w '%{http_code}\n' \
  "https://www.googleapis.com/customsearch/v1?key=$GOOGLE_SEARCH_API_KEY&cx=$GOOGLE_SEARCH_CX&q=steiner"

# Kagi (legacy v0)
curl -sS -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Bot $KAGI_API_KEY" \
  'https://kagi.com/api/v0/search?q=steiner'

# SearXNG
curl -sS -o /dev/null -w '%{http_code}\n' \
  'http://localhost:8888/search?q=steiner&format=json'
```

All four should return 200. A SearXNG 403 can have multiple causes, including disabled JSON or access controls.

3. Start `steiner` and ask something that needs current information, such as "Search the web for the current Go release and summarise the changes". The `web_search` call appears in the transcript. `steiner tools` does not list `web_search` even when it is available, because `web_search` is added per run when a backend is configured.

## Troubleshooting

The tool returns backend errors prefixed with `web_search:`. Brave and SearXNG include at most the first 100 bytes of an error response body; Google and Kagi report the HTTP status without the body, and Google errors may also include the request URL and API key. These errors may be sent to the model provider as tool results. The `curl` commands above discard the body. To inspect it, repeat the relevant command without `-o /dev/null` and handle the output as potentially sensitive.

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| The model never searches, or says it has no search tool | `search.backend` unset, or delegation disabled | Set a backend and leave `sub_agent.enabled` on. |
| `search.backend "x" is not supported` | Typo | Use `brave`, `google`, `kagi` or `searxng`. |
| `search.backend is "brave" but brave_api_key is not set` | Key missing from config and environment | Export the variable or set the config field. The Google, Kagi and SearXNG messages name their own fields. |
| Brave returns 401 | Key wrong, revoked or inactive | Create a key again and check the plan. |
| Brave returns 429 | Rate or credit limit reached | Slow down or add credits. Steiner caps each call at 20 results. |
| Google returns 403 | API not enabled in the key's project, key restricted to another API, or daily quota used | Enable the API, relax the key restriction or wait for the quota reset. |
| Google returns 400 | Bad or missing `cx` | Re-copy the Programmable Search Engine ID. |
| Kagi returns 401 or 403 | Key rejected or no API balance | Check the API portal and billing. |
| SearXNG returns 403 | JSON format not enabled, access controls or automated-client blocking | Check the instance settings and logs. If JSON is disabled, add `json` to `search.formats` and restart the instance. |
| SearXNG connection refused or times out | Container not running, port mismatch, or wrong host | `docker compose ps`, `docker compose logs searxng`, and check `search.searxng_url`. |
| SearXNG returns an empty result list | Upstream engines blocking or throttling the instance | Run the same query in the instance's web UI and check its logs; try another instance. |

## Secrets and networking

- Prefer environment variables or `${VAR}` references in config over literal keys in a file. The [environment variables](configuration.md#environment-variables) section lists the recognised names.
- `steiner config` prints resolved values, including API keys and the SearXNG URL. Google transport and API errors can contain the request URL and API key and may be sent to the model provider as tool results. Do not paste these into bug reports or logs.
- `web_search` honours `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`. `fetch_url` deliberately does not; see [Tools](tools.md#fetching-urls).
