# Configuration internals

User guide: [Configuration](../user/configuration.md).

## Load pipeline

`config.Load` merges, with later sources winning:

1. Compiled defaults (`internal/config/defaults.go`).
2. Global config, `~/.config/steiner/config.yaml`.
3. Project config, `.steiner/config.yaml`.
4. `STEINER_` environment variables.
5. CLI overrides (`load_override.go`). `--unsafe` sets `sandbox.enabled` to false and records the reason in `Sandbox.DisabledBy`.

After the merge, `Load` normalizes paths and execution modes, applies MCP defaults, validates, resolves role assignments for the selected profile, and applies any model override to the active orchestrator model.

Env expansion runs per file before decoding. `readConfigPatch` returns the first file with undefined references, so one error names one file. Within that file, every missing reference is listed, sorted by line, path, and name. Syntax and messages: [environment variable expansion](../user/configuration.md#environment-variable-expansion-in-config-values).

`--config` sets `allowMissing` to false on the project layer, so a missing file fails.

## Project trust

`checkProjectTrust` returns `ErrProjectUntrusted` only when the project file exists and `LoadOptions.ProjectTrust` is not `ProjectTrustTrusted`. The zero value is untrusted. `Load` never reads the trust store; the caller supplies the decision.

`config.InspectProject` produces the inputs for that decision and the display data. It parses the project file to a raw YAML node without expansion or validation, normalizes anchors, aliases, and merge keys, and diffs against the global config and defaults into `ProjectInspection.Changes`, with security-relevant entries first. A YAML error is returned as `ParseError` text. It also reads the trust store, which deletes a corrupt or unsupported-version file and reports `StoreNotice`, so the call has a side effect.

The trust store is `~/.config/steiner/trusted_projects.json`, keyed by absolute, symlink-resolved project root. The directory is mode `0o700` and the file `0o600`. `TrustProject` writes it through `fsatomic.WriteFile`.

`ensureProjectTrust` in `cmd/steiner` resolves trust once per process, memoized on `cliFlags.trustResolved`. Order: stored trust, `--trust-project-config`, `STEINER_TRUST_PROJECT_CONFIG`, the trust dialog (only when not `exec` and the terminal is interactive), then an error naming both fixes. "Always trust" calls `config.TrustProject`. The session choice trusts for the run only. Deny is an error.

`steiner config` loads with `ProjectTrustTrusted`, prints the untrusted-project header from `InspectProject`, and never persists trust. Trust dialog styles come only from user-level TUI preferences, so a project cannot restyle its own trust prompt.

`security_fields_test.go` requires every MCP server field to be covered by an `mcp.servers.*` security pattern or a named exception set, so new fields fail tests until classified.

## Model fact resolution

`internal/provider` resolves six facts separately: context window, max output tokens, vision, reasoning efforts, reasoning echo-back, and transport. The first source in precedence order that answers a fact wins, and lower sources are not consulted for it (`fact_resolver.go`). A source is skipped when none of its facts are pending, so fully configured limits never load models.dev.

| Fact | Precedence |
| --- | --- |
| Context window | config, provider model catalog, discovery (Ollama live probe), models.dev, fallback |
| Max output tokens | config, provider model catalog, discovery (Ollama live probe), models.dev, fallback only when the context window also fell back, otherwise unknown |
| Vision | config, models.dev, unknown |
| Reasoning efforts | config, provider model catalog, models.dev, built-in OpenAI reasoning family table (`builtinSource`), unknown |
| Reasoning echo-back | config, models.dev, unknown (false) |
| Transport | config (including the Codex fixed override), models.dev npm-based override, configured provider type |

The context-window fallback is `defaultContextWindow` at confidence `low`, with the note `conservative default`. `DefaultMaxOutputTokens` gets the same fallback only when the context window also fell back (`applyFactDefaults`). Otherwise an unresolved max output stays unknown. The catalog source reads through the `provider.ModelCatalog` interface (`CatalogModel`).

Generic profiles (`openai_compat`, `ollama`, `litellm`) have no fixed models.dev ID. Their lookup first tries the alias as a strict provider key. Only `not_found` or `provider_mismatch` falls back to `Index.LookupMerged`, which merges all providers' entries: minimum positive context window and max output, AND of vision and echo-back, and the intersection of reasoning efforts. A merged result has confidence `low`, a provider-count note, and no transport override. A malformed strict result is never replaced by a merge. Named providers use their fixed `ModelsDevID`.

## Provenance, notes, and warnings

Each fact carries value, known flag, source, confidence, and an optional note. `ResolvedModel.Facts` exposes them, and `steiner model inspect` prints them in its `facts:` block. Notes record models.dev degradation only for `malformed` and `provider_mismatch`. A `not_found` result records nothing.

Warnings are derived once from final facts (`model_warnings.go`). Only two cases produce one: a source-level error such as `models.dev unavailable: <reason>`, and a context window on the conservative fallback. A degraded lookup result for a fact that a higher source already answered produces a note only. `MetadataSource` and `Confidence` on `ResolvedModel` describe limits provenance only.

## Resolver lifetime

`provider.Resolver` is created per interactive session or CLI run. It memoizes successful resolutions by reference plus a fingerprint of the resolved model and provider config, so an edited alias resolves again. Concurrent identical calls share one resolution. Failures are not memoized. `Invalidate` drops memoized entries and keeps the loaded models.dev index, which is parsed once (`ParseIndex`) and loaded at most once via `sync.Once`.

Related: [prompt caching](prompt-caching.md), [diagnostics](diagnostics.md), [advisor](advisor.md), [MCP](mcp.md), [Claude subscription](claude-subscription.md).
