# Security audit

`/security-audit` is Steiner's built-in static, read-only security review workflow. It maps the attack surface of a git diff or a repository, runs per-domain review finders, adversarially verifies every candidate with a fresh evaluator, and writes one report under `.steiner/security/`.

It is not a scanner. It does not build, test, install, fetch the network, execute exploits, run external tools, or consult CVE and advisory data. It reads source and reasons about reachability, so findings are static hypotheses, not proof.

## Purpose

Use it before shipping a change or when onboarding to unfamiliar code:

- audit what a branch changed, against a resolved base
- audit the repository, or a subtree, as a whole
- get an adversarial second opinion on each candidate before you trust it

The only file the audit writes is the report. It never modifies code, configuration, or `.gitignore`, and never stages, commits, or reverts anything.

## Usage

```
/security-audit diff [base-ref]
/security-audit repo [path]
```

- `diff` audits what changed against a resolved base.
- `repo` audits the repository root, or the subtree at `path`.

With no mode, an unrecognised mode, or an ambiguous argument, Steiner asks which scope to audit and waits before doing any work.

### Diff base and changed set

The base is the `base-ref` you give, otherwise the first ref that resolves, tried in this order: `refs/remotes/origin/HEAD`, `origin/main`, `origin/master`, `main`, `master`. If none resolves, the audit stops and asks for a base.

The changed set is the union of committed changes against the merge base, staged changes, unstaged changes, and non-ignored untracked files, with status, rename, and delete information kept. An empty set is an error; the audit never falls back to repo mode.

### Repo scope

Repo mode audits the repository root or the given subtree, ranking files reachable from the mapped entry points ahead of the rest. The path must exist and resolve inside the repository root.

## Exclusions

Exclusions are always listed with a reason; none are dropped silently.

- Diff mode excludes binary paths, files over 1 MiB, submodules, and anything under `.steiner/`.
- Repo mode excludes `vendor/`, `node_modules/`, `dist/`, `build/`, generated files (a `DO NOT EDIT` header, `*.pb.go`, `*_generated.*`), and `.steiner/` including earlier reports.
- Lockfiles are read only by the supply-chain lens.

Diff mode expands each changed symbol by one hop: direct callers and callees, the nearest entry point, and any middleware or config it names. LSP is used where available, otherwise grep.

## Requirements

- **Delegation must be enabled** (`sub_agent.enabled: true`). The audit needs the `sub_agent` tool to map, find, and verify; there is no inline fallback, so without it Steiner stops and tells you to enable delegation.
- `.steiner/security/` must be writable. If it cannot be created, the audit stops before starting.
- The session's current execution mode is preserved, never switched. Plan mode already permits writes to `.steiner/security/`, so the report can be written there without leaving plan mode.

## Workflow

The audit runs as a fixed multi-agent pipeline:

1. **Map** — one `explore` sub-agent builds an attack-surface map: entry points, trust boundaries, sensitive sinks, authn/authz chokepoints, validation patterns, secret handling, config and deployment files, dependency manifests, and AI/LLM usage signals. It makes no vulnerability judgements.
2. **Lens selection** — lenses are selected from the map. A lens runs only when the map shows relevant surface; every skipped lens is recorded with its reason.
3. **Advisor checkpoint 1** — optional, only when the `advisor` tool is available. Asks whether the map and chosen lenses miss an attack surface or a relevant lens.
4. **Find** — one `review` sub-agent per selected lens, run in parallel.
5. **Synthesise** — parent work. Deduplicates by root cause, flags locations raised by more than one lens, promotes out-of-lens observations, and assigns stable IDs (`SA-001`, `SA-002`, …).
6. **Verify** — one fresh `evaluate` sub-agent per candidate, given the claim as a neutral hypothesis and never the finder's reasoning, confidence, or severity. Each verifier tries to disprove the claim.
7. **Advisor checkpoint 2** — optional, only when the `advisor` tool is available. Asks whether the verdicts and severities hold up. Feedback may trigger a fresh verifier run or a severity change, but never promotes a candidate to `supported` on its own.
8. **Report** — written to disk as described below.

## Lenses

| Lens | Checks |
|------|--------|
| Input handling & injection | String-built queries, commands, and templates; path joins without containment; unsafe deserialisation; unrestricted uploads; `eval`-style execution of untrusted input. |
| AuthN, AuthZ & session | State-changing handlers missing an authorization check; sibling endpoints that do check; unverified object ownership; missing CSRF; session issuance, invalidation, default or bypassable credentials. |
| Cryptography & secrets | Hardcoded credentials or keys; weak or fast password KDFs; ECB mode, static IVs, non-CSPRNG randomness; disabled TLS verification; key material that can leak. |
| Data exposure & privacy | Secrets or PII in logs; stack traces or internals returned to clients; debug features on by default; serialising more fields than the caller needs. |
| Supply chain & dependencies | Unpinned version ranges; missing lockfile; scripts that fetch and execute; deps fetched over plain HTTP; vendored code with no provenance. Hygiene only: no CVE or advisory matching. |
| Configuration & deployment | Containers running as root; secrets baked into images; CI triggers such as `pull_request_target` with checkout of untrusted code; actions unpinned to a SHA; overbroad tokens; IaC exposing public or wildcard access; permissive CORS or debug endpoints. |
| Business logic & resource abuse | Bypassable or repeatable workflows; missing quotas or rate limits; unbounded input driving allocation; replay or missing idempotency on state-changing operations. |
| AI/LLM-specific (conditional) | Untrusted content entering prompts; model output passed to a shell, SQL, or HTML sink; tools or agents granted broader reach than needed. |

## Verdicts, severity, and confidence

Every candidate carries a verdict from a fixed vocabulary:

| Verdict | Meaning |
|---------|---------|
| `supported` | An independent static attempt to disprove the claim failed. Attacker control, reachability, and impact are plausible and no inspected defence blocks the path. |
| `refuted` | Concrete evidence defeats attacker control, reachability, the absence of a defence, or the impact. Refuted candidates go to the report's rejected list, not to findings. |
| `unresolved` | Static source cannot decide, for example because of runtime configuration, deployment, or unreviewed code. Unresolved candidates get their own report section, never Findings. |

`supported` means the attempt to disprove failed; it is not proof. Steiner never calls code "secure" or "safe", and never labels a verdict "confirmed".

Severity is the impact if the issue is real:

| Severity | Meaning |
|----------|---------|
| `Critical` | Remote code execution, full authentication bypass, or mass exposure of data or credentials. |
| `High` | Scoped privilege gain or data exposure, or injection with limited reach. |
| `Medium` | Narrow logic flaw, weak cryptography, or a missing defence-in-depth control. |
| `Low` | Hardening gap or minor information disclosure. |

The finder's confidence (`high`/`medium`/`low`) is informational only and does not affect the verdict. There is no CVSS scoring.

## Report

The report is written to `.steiner/security/YYYY-MM-DD_HHMM_<diff|repo>.md` in local time. It is create-only: if the name is taken, `_2`, `_3`, … are appended until free, and an existing report is never overwritten. No other file is written.

A report contains:

- title and one-line scope
- scope and methodology: mode, base and `HEAD` SHAs or path, timestamp, changed vs context files or mapped vs deep-reviewed files, exclusions with reasons, lenses run and skipped with reasons, and which advisor checkpoints ran
- a plain-language summary with counts by severity and by verdict
- findings (`supported` only), ordered by severity, each with ID, title, lens, severity, finder confidence, verdict, location, attacker and entry point, path to impact, evidence, defences checked, and remediation in prose (no patches)
- unresolved claims with the same fields plus what would decide them
- rejected candidates, one line each with the reason
- not reviewed: coverage gaps in repo mode
- limitations

Reports never quote a secret value; secrets are masked with their type and location. They describe vulnerability classes and paths, never runnable exploit payloads. When nothing is found the report says so within the reviewed scope and lenses, without claiming the code is secure.

After writing, Steiner summarises in chat: scope, counts, blockers or coverage gaps, the report path, and a one-line suggestion to add `.steiner/security/` to `.gitignore` if reports should not be committed.

## Model configuration

The verifier is the deepest-reasoning step, so the `evaluate` sub-agent can be assigned a stronger or different model than the rest of the audit:

```yaml
models:
  profiles:
    default:
      sub_agents:
        evaluate: anthropic/<evaluate-model-id>
```

If no override is set, `evaluate` uses the profile's `default_model`. See [Recommended model tiers](sub-agent-delegation.md#recommended-model-tiers) for how the agent types map to model tiers.

## Cost and limits

The audit has no built-in caps: no limit on the number of finders, verifiers, or context files pulled in by the one-hop expansion. Cost and latency scale with the scope you audit, so a whole-repository run on a large codebase can be expensive. Start with a diff or a subtree when in doubt.

Limitations to keep in mind:

- static review only; nothing is executed or dynamically tested
- no CVE or advisory data, so known-vulnerable dependency versions are not matched
- the one-hop context heuristic can miss multi-hop source-to-impact paths
- verifier independence can be limited by configuration

## Safety

- The only file the audit writes is the report; nothing else is ever created, modified, or deleted.
- When sandboxing is enabled, the mapper and verifiers run with the project mounted read-only. Finders don't get a read-only mount in build mode, so for them read-only is an instruction in the brief, not an enforced restriction. In plan mode the whole project is read-only apart from `.steiner/plans/`, `.steiner/security/`, and `.git` when sandboxed.
- Repository files and tool output are treated as untrusted data, never as instructions.
- No exploit payloads, no secret values, no "secure" claims.
- Execution mode is never switched; plan mode stays plan mode.
