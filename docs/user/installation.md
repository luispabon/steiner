# Installation

## Prerequisites

- On Linux, the sandbox needs `bubblewrap` (`bwrap`) on `PATH`. Without it, tools run unsandboxed and the sandbox is reported as `unavailable`. See [Sandboxing](sandboxing.md).
- `git` is needed for sub-agent delegation worktrees and oneshot runs.

The sandbox is Linux-only. On macOS and Windows, tools run without it. The release binary has no other dependencies.

## Download a release binary

Download the binary for your operating system and CPU from [GitHub Releases](https://github.com/luispabon/steiner/releases):

| Platform | Asset |
|----------|-------|
| Linux x86-64 | `steiner-linux-amd64` |
| Linux ARM64 | `steiner-linux-arm64` |
| macOS Intel | `steiner-darwin-amd64` |
| macOS Apple silicon | `steiner-darwin-arm64` |
| Windows x86-64 | `steiner-windows-amd64.exe` |

Each release also has a `steiner_<version>_checksums.txt` file with SHA-256 sums. Download it from the same release and check the line for your asset, for example:

```bash
sha256sum --check --ignore-missing steiner_<version>_checksums.txt
```

On Linux and macOS, make the file executable, rename it to `steiner` if you like, and put it in a directory on your `PATH`. No package manager or fixed install location is needed. Check the installation:

```bash
steiner version
```

Next, configure a provider and start a session: see [Quickstart](quickstart.md).

## Install with Go

With the Go version named in [`go.mod`](https://github.com/luispabon/steiner/blob/main/go.mod) or newer:

```bash
go install github.com/luispabon/steiner/cmd/steiner@latest
```

This builds from source. The binary reports its version as `dev`.

## Build from source

Clone the repository, then build `bin/steiner` with `make build-binaries`, or build or run it directly with Go:

```bash
make build-binaries
go build ./cmd/steiner
go run ./cmd/steiner
```

## Updating

`steiner update` (alias `steiner upgrade`) updates the binary that is running:

```bash
steiner update            # latest stable release
steiner update v1.2.0     # a specific release; 1.2.0 also works
steiner update --dev      # latest dev build
```

Steiner downloads the release asset for the current platform, verifies it against the release checksums file, and replaces the running executable. If the replacement fails, the previous binary is restored. The directory holding the executable must be writable by your user.

If the running version is already the latest stable release, `steiner update` reports `Up to date` and changes nothing.

The updater always installs a release binary over whatever is running; it does not know about source checkouts. A binary built with `go install` or `go build` reports version `dev`, and `steiner update` replaces it with the latest stable release. To stay on a source build, rebuild it instead.

### Release channels

- **stable** (default): tagged releases.
- **dev**: the `dev` prerelease, rebuilt from every push to `main`. `steiner update --dev` (or `steiner --dev update`) always reinstalls the current dev build.

`--dev` cannot be combined with a version. When the requested channel differs from the running build's channel, Steiner warns that the current build will be replaced.

### GitHub token

Update checks use the GitHub API, which applies rate limits to anonymous requests. Set `STEINER_GITHUB_TOKEN` to use a token:

```bash
export STEINER_GITHUB_TOKEN=ghp_...
```

### Network timeouts

Update requests fail instead of hanging: release lookups time out after 30 seconds, the checksums file after 60 seconds and the binary after 15 minutes. A request that makes no progress for 30 seconds is abandoned.

### Update notices

The interactive TUI checks for a newer release in the background at startup and shows a notice in the sidebar when one is available. `steiner --exec` and other commands do not check. Configure or disable the check with the [`update_check` block](configuration.md#update_check-block).
