# Installation

## Download a release binary

The supported user path is a binary from [GitHub Releases](https://github.com/luispabon/steiner/releases). Download the asset matching your operating system and CPU architecture:

- `steiner-linux-amd64`
- `steiner-linux-arm64`
- `steiner-darwin-amd64`
- `steiner-darwin-arm64`
- `steiner-windows-amd64.exe`

Choose the OS and architecture for the machine where you will run Steiner. Download the matching `steiner_<version>_checksums.txt` file from the same release. Verify that the checksum of the binary matches the line for the downloaded asset, using the checksum tool provided by your operating system. Do not use a checksum file from another version.

On Unix systems, mark the downloaded file executable. Put it in any directory already on your `PATH`, or add its directory to `PATH` using the normal mechanism for your shell. Steiner does not require a package manager or a particular installation directory.

Check the installation:

```text
steiner version
```

See [Updates](updates.md) for the built-in release updater and [Getting started](getting-started.md) for the first run.

## Build from source

Build from source only when contributing or when no release asset supports your platform. The repository requires Go `1.26.6`.

Check out the repository, then either build release binaries with `make build-binaries` or run the program directly:

```bash
make build-binaries

go run ./cmd/steiner
```

Source checkouts should be rebuilt when you want a newer version. The release updater is for installed release binaries, not a source-build workflow.
