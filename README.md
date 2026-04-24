# steiner

`steiner` is a minimal, local-first coding agent in Go. It is built to work against real local repositories with bounded context, explicit approvals, and OpenAI-compatible providers, including a local server on `http://localhost:11434/v1`.

## Today in brief

* Single-agent loop with interactive terminal mode and `--exec`
* Config, tools, skills, and version commands are available now
* Default provider targets a local OpenAI-compatible endpoint
* Mutating tools are approval-gated by default; reads are auto-approved

## Quickstart

Requirements: Go `1.24+`.

1. Start from source in this repo.
2. Make sure an OpenAI-compatible server is running at `http://localhost:11434/v1`, or override the provider settings in config.
3. Run a first request from source:

```bash
go run ./cmd/steiner --exec "summarize this repository in one sentence"
```

4. Optional: inspect the resolved configuration before you do real work:

```bash
go run ./cmd/steiner config
```

If you want a local binary:

```bash
make build-binaries
./bin/steiner
```

## Common commands

* `go run ./cmd/steiner` or `./bin/steiner` - interactive mode
* `go run ./cmd/steiner --exec "..."` - run one request and exit
* `go run ./cmd/steiner version` - print the build version
* `go run ./cmd/steiner config` - print the resolved configuration
* `go run ./cmd/steiner tools` - list configured tools
* `go run ./cmd/steiner skills` - list discovered skills

## Configuration

Configuration precedence is:

1. compiled defaults
2. `~/.config/steiner/config.yaml`
3. `.steiner/config.yaml`
4. environment variables with the `STEINER_` prefix
5. CLI flags

Key environment variables:

* `STEINER_MODEL`
* `STEINER_SCHEDULER_PARALLELISM`
* `STEINER_MAX_TURNS`
* `STEINER_MAX_TOKENS`
* `STEINER_LOG_LEVEL`
* `STEINER_LOG_FILE`
* `STEINER_TOOL_OUTPUT_MAX_BYTES`

### Config format

`model` selects the active alias from `models`. Each model entry holds the OpenAI-compatible provider settings for that alias, including compaction budgets.

```yaml
scheduler:
  parallelism: 2

model: default

models:
  default:
    type: openai_compat
    base_url: http://localhost:11434/v1
    api_key: ""
    model: <required-backend-model>
    temperature: 0.2
    max_completion_tokens: 8192
    context_size: 32768
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 1024
  fast:
    type: openai_compat
    base_url: http://localhost:11434/v1
    api_key: ""
    model: yi-coder:9b
    temperature: 0.1
    max_completion_tokens: 4096
    context_size: 16384
    compaction:
      safety_margin_tokens: 1024
      summary_max_tokens: 512

limits:
  max_turns: 50
  max_tokens: 500000
  tool_timeout_default: 30s
  tool_timeouts:
    bash: 2m0s
    read: 5s
    write: 5s
  tool_output_max_bytes: 65536

approval:
  default: prompt
  overrides:
    read: auto
    glob: auto
    search: auto
    write: prompt
    bash: prompt

sub_agent:
  enabled: false
  max_turns: 15
  max_tokens: 100000
  allowed_tools:
    - read
    - glob
    - search
    - write
    - bash
  allow_nesting: false
  max_concurrent: 1

tools:
  repo-lint:
    exec: /usr/local/bin/repo-lint
    subcommand: repo-lint
    description: Runs the repo linter and reports actionable failures.
    parameters:
      type: object
      properties:
        path:
          type: string
      required:
        - path
    timeout: 30s
    approval: auto
    constraints:
      allowed_dirs:
        - /home/luis/Projects/AI/steiner

project_context:
  max_tokens: 2000
  extra_files:
    - README.md
    - AGENTS.md
  ignore_files:
    - "*.lock"
    - node_modules/

paths:
  project_root_only: true
  writable_paths:
    - /tmp/steiner
  blocked_paths:
    - ~/.ssh

logging:
  level: info
  file: ~/.local/share/steiner/steiner.log
```

Approval defaults are conservative: `read`, `glob`, and `search` are auto-approved; mutating actions like `write` and `bash` prompt first. For most installs, the minimum useful config is just `model`, one `models.<alias>` entry, and any overrides you actually need in `limits`, `approval`, or `logging`.

## Build and test

```bash
go build ./...
go test ./...
make build-binaries
go vet ./...
```

## Development notes

Repo layout is compact: `cmd/` for entrypoints, `internal/` for agent/provider/tool/config code, `docs/` for product docs, and `testdata/` for fixtures. Conventions and deeper repo rules live in [AGENTS.md](AGENTS.md).

## Further reading

* [AGENTS.md](AGENTS.md)
* [docs/PRD.md](docs/PRD.md)
* [docs/ROADMAP.md](docs/ROADMAP.md)
* [docs/INITIAL_IMPLEMENTATION_PLAN.md](docs/INITIAL_IMPLEMENTATION_PLAN.md)
