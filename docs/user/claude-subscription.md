# Claude subscription

`claude_subscription` runs the user's signed-in `claude` CLI and uses the Claude subscription available to that login. It is an unofficial Steiner integration and is not affiliated with Anthropic.

Anthropic can change the CLI, subscription terms, or supported behavior. Before enabling this provider, read Anthropic's current compliance guidance:

- [Use the Claude Agent SDK with your Claude plan](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan)
- [Claude legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)

## Requirements and setup

1. Install Claude Code and make sure the `claude` command is on `PATH`.
2. Sign in as the account that should pay for and own the subscription usage:

   ```bash
   claude auth login
   ```

3. Use a full model ID reported by the signed-in CLI. Do not use a display name or a short alias supplied by another provider.
4. Configure a provider with `type: claude_subscription`. Do not add `base_url`, `api_key`, `api_key_env`, or provider headers.

```yaml
providers:
  claude:
    type: claude_subscription

models:
  profiles:
    default:
      default_model: claude/<full-model-id>
```

The provider requires Claude CLI version `2.1.294` or newer. The login must be a `claude.ai` first-party subscription login. Pro, Max, Team, and Enterprise plans are accepted when the signed-in account exposes them through the CLI.

An API-key login, Bedrock, Vertex, or Foundry is not a Claude subscription login for this provider. Steiner refuses those identities. Use the `anthropic` provider with the matching API credentials instead. A oneshot run also refuses to use this provider: configure another provider for oneshot roles and runs.

## Extra usage safety gate

Before sending a user turn, Steiner checks the account's usage state. The check passes only when rate-limit information is available and extra usage, also called usage credits, is explicitly disabled. Usage state that cannot be confirmed yet, such as missing, null, or malformed fields, is checked again for about 15 seconds, since a fresh session can report it before its metadata loads. Each usage request still has the 30-second CLI control timeout. If the live CLI process still cannot confirm the state, Steiner re-checks it once with a brand-new CLI process, which reads the state directly instead of reusing the CLI's cached answer; that process's startup and usage read are bounded to 8 seconds, and its teardown uses the normal session close timeout. If the state still cannot be confirmed, Steiner fails closed; a refusal based on an unconfirmable usage reply names the missing state in parentheses. If extra usage is enabled, Steiner refuses the turn at once rather than allowing paid usage.

Turn off extra usage in Claude at **Settings -> Usage**, then retry. This gate is strict because Anthropic may change the account response or subscription behavior. It is separate from the ordinary subscription limit: a normal limit can still stop a turn and report its reset time.

## What Steiner controls

Steiner owns the model request, system prompt, tool definitions, tool approval policy, sandbox policy, output limits, and session lifecycle. The `claude` process performs the subscription-backed model request. Steiner does not grant the CLI access to tools beyond the MCP tools it publishes for that session, and the CLI is started with settings that disable its own tool prompts for these published tools.

Steiner starts the CLI with a private temporary session directory and a private system-prompt file. Tool calls return through a loopback MCP endpoint owned by Steiner. Steiner keeps tool output within its configured output budget and does not forward API credential environment variables to the child process.

Preflight fitting for `claude_subscription` uses the same token estimate as other providers, so the local ctok count for the three exact Claude IDs applies here too. The count is still approximate and omits CLI and tool framing; see [Token estimation](context-management.md#token-estimation). Authoritative CLI-reported prompt usage and the existing stateful compaction policy are unchanged. Subscription calibration is not added; it is deferred to [steiner issue 939](https://github.com/luispabon/steiner/issues/939).

## Context the CLI adds

Steiner turns off these CLI context sources in every `claude_subscription` request: auto-memory, `CLAUDE.md` files (project, local, and user), the skills listing, token-count and todo reminders, "user hasn't heard from you" nudges, and git instructions.

The CLI still sends the following to the model. Steiner cannot turn them off:

- Account email: the email address of the signed-in account, in a system reminder on every request. Only bare or simple mode and `ANTHROPIC_UNIX_SOCKET` remove it, and both disable subscription login.
- Environment: working directory, whether it is a git repository, platform, shell, OS version, and the model name and knowledge cutoff.
- Date: a "Today's date" line that duplicates Steiner's own.
- An SDK identity line before Steiner's system prompt.
- Account, project, or git context, re-announced if it changes during a session.

## Session continuity

Only parent normal calls persist CLI session state. Steiner stores that state as opaque backend data with the parent conversation. Advisor, sub-agent, and compaction helper processes never persist CLI state.

For each parent turn, Steiner either:

- appends the new messages to the live CLI session, when it matches the Steiner transcript. A live session keeps the system prompt and tool set it started with, even if they have since changed;
- resumes the stored CLI session after a restart, when it matches the session owner and transcript;
- forks the stored CLI session, when the session owner changed, such as after a Steiner Fork;
- reseeds a CLI session from the Steiner transcript, when the restored stored state is missing, dead, mismatched, or was built for a different system prompt or tool set. A session that is still live is not reseeded for a changed prompt or tool set.

A reseed sends the non-system history as one transcript message. Prompt cache reuse is not expected for a reseeded turn. Images still held in memory are included. Image data stripped after it was consumed cannot return. Earlier tool calls appear as text.

A compaction summary that Steiner appends in place rewrites that CLI session, so Steiner retires it and drops the stored state for the generation the summary replaced. The clear also applies when the summary fails or is cancelled. In that case the conversation and its generation stay as they were, and the next request reseeds from the preserved Steiner transcript. A forked summary leaves the live session and its stored state alone, and a compaction fallback never clears the main session state. With no live session, a helper builds the summary, and a successful helper summary also carries the clear for the replaced generation. Stored state is saved with the session, so a crash during a compaction can leave the older saved state in place. That saved checkpoint may be out of date, and how resume behaves after such a crash is unverified.

## Privacy and telemetry

Prompts, system instructions, images that are accepted by the provider, tool calls, and tool results are sent to the local `claude` process. The process then handles the network request under the signed-in Claude account. Claude's own data handling, telemetry, retention, and account controls apply; see Anthropic's legal and compliance documentation linked above. The email address of the signed-in account is also sent to the model on every request, and Steiner cannot prevent it. See [Context the CLI adds](#context-the-cli-adds).

Steiner does not read the Claude credential file or copy its token into provider configuration. It checks `claude auth status`, starts the CLI, and removes the credential and CLI entrypoint variables from the child environment. Steiner's own logging and diagnostics settings still apply. Logs and diagnostics can contain prompts or tool output when enabled, so review `logging` and `diagnostics` settings before using this provider.

## Model selection

Model discovery uses the signed-in CLI's `initialize` control response and lists the full resolved model IDs available to that account. If discovery fails or returns no usable models, Steiner uses a static fallback catalog so the provider remains selectable. See [Model enumeration](model-enumeration.md#claude-subscription) for the timeout and fallback behavior.

You can select a discovered model with `/model`, or use a raw reference such as `claude/<full-model-id>`. The provider can switch model and reasoning effort during a live session. The model switch is sent to the existing CLI process, not implemented by adding a model name to the pool key.

## Limitations

- Previously sent history is not rewritten in the CLI. Changes to sent messages, other than removal of consumed image data, are not patched into the live session. Steiner reseeds instead, as described in Session continuity. In an interactive conversation, `/clear` starts a new empty Steiner and Claude CLI session; it does not rewrite the prior transcript. There is no user-facing rewind.
- Support for changing already-sent history is tracked in [#895](https://github.com/luispabon/steiner/issues/895).
- The CLI owns its process identity and transcript. Its identity or reminders can change as the CLI changes, and switching models can reset model-specific cache state. Do not treat those details as stable Steiner storage. The CLI also adds context that Steiner cannot disable; see [Context the CLI adds](#context-the-cli-adds).
- The provider is not a replacement for the native `anthropic` API provider. It uses the signed-in CLI and the subscription's account controls.
- If the Claude CLI ends a response and then starts a second response within the same turn, possibly after its safety safeguards refused the request, Steiner reports that the CLI began another response and stops the affected response.
- Claude CLI and Anthropic can change these behaviors without a corresponding Steiner release. Recheck the compliance links and CLI version when an account or model stops working.
