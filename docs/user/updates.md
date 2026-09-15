# Updates

An installed release binary can self-update with `update` or its `upgrade` alias. It selects the runtime asset, verifies its checksum, and atomically replaces the running executable. The executable's location must be writable by the user running the update. A source checkout is not a release-binary install: rebuild it instead with the source instructions in [Installation](installation.md#build-from-source).

```bash
steiner update
steiner update v1.2.0
steiner update 1.2.0
```

A GitHub token is optional, but API rate limits may apply without one:

```bash
export STEINER_GITHUB_TOKEN=ghp_...
```

## Release channels

Stable is the default channel. Use `--dev` to install the build published from each merge to `main`:

```bash
steiner update --dev
steiner --dev update
```

`--dev` and a specific version cannot be used together. When switching channels, Steiner warns that the current build will be replaced.

## Passive checks

Interactive startup checks GitHub for a newer release at most once per `update_check.interval_hours` and shows a sidebar notice when one is available. Set `update_check.enabled: false` to disable the check.
