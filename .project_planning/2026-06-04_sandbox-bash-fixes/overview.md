## Request

Fix sandbox bash tool issues discovered during bubblewrap integration:
1. Persistent `cd` error + first stdout line swallowed (Issues 1 & 2 from investigation)
2. `go` toolchain inaccessible inside sandbox
3. `/etc/hosts` leaks host network topology (lower priority)

Source investigation: `.project_planning/bash_tool_issues.md`

## Overview

Three fixes in `internal/sandbox/`, one in `internal/tool/builtin/`:

### Fix A — CWD + output capture (Issues 1 & 2)

Root cause: `WrapCommand` copies `Dir: cmd.Dir` (empty). Bwrap inherits host CWD which doesn't exist in the namespace. Bash emits a `cd` diagnostic on startup that corrupts marker-based output capture, swallowing the first line of every command.

Fix: Add `--chdir /workspace` to bwrap args in `BuildArgs()`. This is bwrap's native CWD mechanism — sets CWD after namespace setup, before exec. Cleaner than setting `Dir` on the host-side `exec.Cmd`.

### Fix B — Go toolchain access

Three paths missing from sandbox mounts:
- `GOROOT`: `~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.4.linux-amd64` — the active Go toolchain
- `GOMODCACHE`: `~/go/pkg/mod` — downloaded modules (superset of GOROOT path)
- `GOCACHE`: `~/.cache/go-build` — build cache

Since `GOMODCACHE` (`~/go/pkg/mod`) contains `GOROOT`, mounting `~/go` ro covers both GOROOT and GOMODCACHE. GOCACHE needs write access for builds.

Approach:
1. Mount `~/go` → `/home/steiner/go` as **ro-bind** (modules + toolchain)
2. Mount `~/.cache/go-build` → `/home/steiner/.cache/go-build` as **rw bind** (build cache needs writes)
3. Add `GOPATH`, `GOROOT`, `GOCACHE`, `GOMODCACHE` to env allowlist in `env.go`, remapping `$HOME`-prefixed paths from host home to `/home/steiner`

Path remapping: Go env vars reference `/home/luis/...` but sandbox HOME is `/home/steiner`. `FilterEnv` must rewrite these paths. Alternative: set explicit `--setenv` overrides in `BuildArgs` using sandbox-relative paths, avoiding the need for path remapping in `FilterEnv`.

Chosen approach: `--setenv` in `BuildArgs` — simpler, no regex/string replacement in FilterEnv, and the values are deterministic from the mount layout.

### Fix C — `/etc/hosts` sanitization

Current: host `/etc/hosts` is ro-bound, exposing internal hostnames and IPs.

Fix: Write a minimal `/etc/hosts` to the sandbox home dir at startup, bind that instead of the host file. Contents: just `localhost` entries.

## Verification Strategy

### Repo-mandated checks
- `make check` — runs fmt, vet, lint, test, build (medium cost)
- `go test ./internal/sandbox/... -run TestName` — targeted (cheap)
- `go test ./internal/tool/builtin/... -run TestBash` — targeted (cheap)
- `gofmt -w <files>` — formatter (cheap, safe-fix mode)
- `goimports -w <files>` — import organizer (cheap, safe-fix mode)
- `golangci-lint run ./internal/sandbox/...` — lint (cheap)

### Manual verification
- Run steiner with sandbox enabled, execute bash commands, verify:
  - No `cd` error on stderr
  - First line of stdout not swallowed
  - `go version`, `go build ./...`, `go test ./...` work
  - `/etc/hosts` inside sandbox contains only localhost entries

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | Use `--chdir /workspace` not `Dir` field | bwrap-native, sets CWD after namespace mount setup |
| 2 | Mount `~/go` ro, not rw | Modules should not be modified from sandbox |
| 3 | Mount `~/.cache/go-build` rw | Build cache requires writes; safe to allow |
| 4 | Use `--setenv` for Go vars, not FilterEnv remapping | Deterministic from mount layout, no string manipulation |
| 5 | Generate minimal `/etc/hosts` in sandbox home | Avoids host info leak without breaking DNS resolution |
| 6 | Keep `/etc/resolv.conf` from host | Required for DNS; contains only nameserver IPs, low risk |
