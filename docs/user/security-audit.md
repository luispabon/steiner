# Security audit

`/security-audit` is a bundled [skill](skills.md) that statically reviews a git diff or a repository for security weaknesses and writes one report under `.steiner/security/`.

It is not a scanner. It does not build, test, install, use the network, run exploits, run external tools, or consult CVE and advisory data. It reads source and reasons about reachability, so findings are informed hypotheses, not proof.

## Usage

```text
/security-audit diff [base-ref]
/security-audit repo [path]
```

- **`diff`** audits what your branch changed: committed changes since the merge base, plus staged, unstaged, and untracked (non-ignored) files. The base is `base-ref` if given, otherwise the first that resolves of `refs/remotes/origin/HEAD`, `origin/main`, `origin/master`, `main`, `master`. If none resolves, or nothing changed, the audit stops and tells you.
- **`repo`** audits the whole repository, or the subtree at `path` inside it.

With no mode or an unclear argument, Steiner asks which scope to audit before doing any work.

Invoking `/security-audit` does not change the [execution mode](working-with-steiner.md#plan-and-build-modes). Plan mode allows writes to `.steiner/security/`, so the audit works in either mode.

## Requirements

- Delegation enabled (`sub_agent.enabled: true`). The audit uses sub-agents to map, find, and verify, and stops if they are unavailable. See [Sub-agent delegation](sub-agent-delegation.md).
- A writable `.steiner/security/`. The audit creates the directory if it is missing and stops if it cannot.

## How it works

An `explore` sub-agent maps the attack surface. Steiner then picks the relevant security lenses (for example injection, authorisation, secrets, supply chain, configuration) and runs one `review` sub-agent per lens in parallel. Each candidate finding goes to a fresh `evaluate` sub-agent that tries to disprove it without seeing the finder's reasoning. When the [advisor](advisor.md) is enabled, it is consulted on the lens choice and on the final verdicts.

The full workflow, lens list, and report format are in the skill file: [skills/security-audit/SKILL.md](https://github.com/luispabon/steiner/blob/main/skills/security-audit/SKILL.md).

## Verdicts and severity

| Verdict | Meaning |
|---|---|
| `supported` | An independent attempt to disprove the claim failed: attacker control, reachability, and impact look plausible and no inspected defence blocks the path. Listed under Findings. |
| `unresolved` | Source alone cannot decide, for example because it depends on runtime configuration or unreviewed code. Listed in its own section. |
| `refuted` | Concrete evidence defeats the claim. Listed as rejected with the reason. |

`supported` is not proof. The report never calls code "secure" or "safe".

Severity is the impact if the issue is real: `Critical` (remote code execution, full authentication bypass, mass data or credential exposure), `High`, `Medium`, or `Low` (hardening gap or minor disclosure). There is no CVSS score.

## The report

The report is written to `.steiner/security/YYYY-MM-DD_HHMM_<diff|repo>.md` in local time. If that name exists, `_2`, `_3`, and so on are appended; an existing report is never overwritten.

It covers scope and method (including excluded files and skipped lenses, with reasons), a summary with counts, findings with location, attack path, evidence, and remediation in prose, unresolved claims, rejected candidates, coverage gaps, and limitations. Secret values are masked, and no runnable exploit payloads are included.

Afterwards Steiner posts a short summary in chat with the report path, and suggests adding `.steiner/security/` to `.gitignore` if you do not want reports committed.

## Cost

There are no built-in caps on the number of finders, verifiers, or context files. Cost and time grow with the scope, so a whole-repository audit of a large codebase can be expensive. Start with `diff` or a subtree.

The verifier does the deepest reasoning, so consider a stronger model for `evaluate` in your profile's `sub_agents` map; see [Recommended model tiers](sub-agent-delegation.md#recommended-model-tiers).

## Safety and limitations

- The audit writes only the report and the `.steiner/security/` directory. It does not modify code, configuration, or `.gitignore`, and does not stage, commit, or revert.
- With the sandbox active, the mapper and verifiers run `bash` with the project mounted read-only. In `build` mode the finders do not get a read-only mount; their read-only behaviour is an instruction, not enforcement. In `plan` mode every sub-agent gets the read-only mount. See [Sandboxing](sandboxing.md).
- Repository content and tool output are treated as untrusted data, never as instructions.
- Static review only: nothing is executed, known-vulnerable dependency versions are not matched, and source-to-impact paths spanning many calls can be missed.
