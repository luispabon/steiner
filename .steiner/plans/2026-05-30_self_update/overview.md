## Request

Implement a `steiner update` command (with `steiner upgrade` as an alias) that checks GitHub Releases for the newest version, and if a newer release exists, downloads the correct binary asset, verifies its SHA256 checksum, and replaces the running executable.

## Overview

### Scope

1. **New `internal/update/` package** — core self-update logic, isolated from CLI wiring:
   - Query GitHub Releases API (`GET /repos/{owner}/{repo}/releases/latest`) for the latest non-draft, non-prerelease release.
   - Match the correct asset name: `steiner-{GOOS}-{GOARCH}` (append `.exe` on Windows).
   - Parse release tag as semver and compare against the compiled-in `version`.
   - Download the matched asset and the corresponding `steiner_{version}_checksums.txt`.
   - Verify the downloaded binary against the parsed SHA256 checksum.
   - Atomically replace the running binary using a rename-based swap (rename old → `.old`, new → target). Rollback on failure.
   - On Windows, hide the leftover `.old` file if it cannot be deleted (running executable is locked).

2. **New `cmd/steiner/update.go`** — Cobra command wiring:
   - Register `update` and `upgrade` aliases in `newRootCommand()`.
   - Skip with a warning when `version == "dev"` (cannot compare).
   - Support optional `STEINER_GITHUB_TOKEN` env var for authenticated API requests (higher rate limits).
   - Print progress/status to stdout (latest version found, downloading, verifying, replacing, done).
   - Return a clear error on any failure (network, asset not found, checksum mismatch, permission denied, etc.).

3. **Update `.github/workflows/release.yml`** — generate and upload `steiner_{version}_checksums.txt` alongside binaries:
   - Compute SHA256 for each built binary.
   - Format: `<hex>  <filename>` (two spaces, one line per file).
   - Upload the checksums file as a release asset.

### Constraints

- Keep package boundaries strict. `internal/update` must not depend on `cmd/steiner` or Cobra.
- Use only the Go standard library + `syscall` for hiding the `.old` file on Windows. No external self-update libraries.
- The release workflow currently uploads raw binaries with no checksums; adding checksums is a breaking change only for the updater (which did not exist before), so it is safe.
- On Windows, the running `.exe` cannot be deleted but can be renamed. The replacement relies on this Windows-specific behavior.

### Risks

- **Permissions**: The process needs write access to its own binary directory. If steiner is installed in a protected directory (e.g., `/usr/local/bin` on macOS or `Program Files` on Windows), the replacement may fail. Mitigation: return a clear error and suggest running with elevated permissions.
- **Gatekeeper / code signing**: Replacing a signed binary on macOS or Windows may invalidate signatures and trigger security warnings on the next run. Out of scope for this initial implementation; noted as a known limitation.
- **Rollback inconsistency**: If the process crashes between the two renames, the target binary path may be missing. Mitigation: verify checksum before starting the swap; keep the `.old` file until the new file is confirmed in place.
- **GitHub API rate limits**: Unauthenticated requests are limited to 60/hr per IP. Optional `STEINER_GITHUB_TOKEN` mitigates this.

## Verification Strategy

The repository uses `make check` as the canonical full verification command. Relevant sub-targets for this feature:

| Command | Purpose | Cost | Fix mode |
|---|---|---|---|
| `make fmt` | Format all Go files with `gofmt` and `goimports` | Cheap | Yes (mutates) |
| `make fmt-check` | Check formatting without mutating | Cheap | No |
| `make imports` | Fix imports with `goimports` | Cheap | Yes (mutates) |
| `make imports-check` | Check imports without mutating | Cheap | No |
| `go test ./...` | Run all unit tests | Medium | No |
| `go test -race ./...` | Run tests with race detector | Expensive | No |
| `go vet ./...` | Static analysis | Cheap | No |
| `golangci-lint run ./...` | Full lint | Medium | No |
| `govulncheck ./...` | Vulnerability scan | Medium | No |
| `make build-binaries` | Build `bin/steiner` | Cheap | No |
| `make check` | All of the above in sequence | Expensive | No |

For this feature, the implementer should run at minimum:
1. `make fmt` and `make imports` after code changes.
2. `go test ./internal/update/...` and `go test ./cmd/steiner/...` (targeted).
3. `make build-binaries` to ensure the binary compiles.
4. `make check` before final commit.

For the release workflow change, there is no automated CI test for the workflow itself. Verification is manual inspection of the workflow YAML and a test release (or `act` local simulation if available).

## Decision Log

1. **Command names**: `update` primary, `upgrade` alias. Both are common in CLI tools (e.g., `brew update` vs `brew upgrade`, but many tools treat them as synonyms). The user explicitly requested both.
2. **Skip dev builds**: When `version == "dev"`, print a warning and exit without checking. There is no reliable semver to compare.
3. **Windows self-replacement**: Use the `os.Rename` swap pattern (rename current → `.old`, new → target). This is the standard pattern used by `minio/selfupdate` and is well-tested. On Windows, `os.Remove` on the `.old` file will fail because the running process holds the handle; hide the file instead using `syscall.SetFileAttributes`. We will use `syscall` to avoid adding a dependency.
4. **Checksum source**: Download `steiner_{version}_checksums.txt` from the release assets and parse it. Do not rely solely on GitHub's newer API `assets[].digest` field (introduced June 2025) because it is less mature and does not protect against a compromised release process. The checksums file also provides a natural extension point for future signing.
5. **No external dependencies**: Implement the update logic in `internal/update` using only the standard library. This keeps the binary lightweight and avoids supply-chain risks from third-party update libraries.
6. **Optional `STEINER_GITHUB_TOKEN`**: Support an environment variable for authenticated API requests. This is simpler than adding a config field and covers the main use case (corporate NAT, frequent checks).
