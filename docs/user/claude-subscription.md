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

Before sending a user turn, Steiner checks the account's usage state. The check passes only when rate-limit information is available and extra usage, also called usage credits, is explicitly disabled. Missing, null, malformed, or otherwise unconfirmed state fails closed. If extra usage is enabled, Steiner refuses the turn rather than allowing paid usage.

Turn off extra usage in Claude at **Settings -> Usage**, then retry. This gate is strict because Anthropic may change the account response or subscription behavior. It is separate from the ordinary subscription limit: a normal limit can still stop a turn and report its reset time.

## What Steiner controls

Steiner owns the model request, system prompt, tool definitions, tool approval policy, sandbox policy, output limits, and session lifecycle. The `claude` process performs the subscription-backed model request. Steiner does not grant the CLI access to tools beyond the MCP tools it publishes for that session, and the CLI is started with settings that disable its own tool prompts for these published tools.

Steiner starts the CLI with a private temporary session directory and a private system-prompt file. Tool calls return through a loopback MCP endpoint owned by Steiner. Steiner keeps tool output within its configured output budget and does not forward API credential environment variables to the child process.

## Session continuity

Only parent normal calls persist CLI session state. Steiner stores that state as opaque backend data with the parent conversation. Advisor, sub-agent, and compaction helper processes never persist CLI state.

For each parent turn, Steiner either:

- appends the new messages to the live CLI session, when it matches the Steiner transcript. A live session keeps the system prompt and tool set it started with, even if they have since changed;
- resumes the stored CLI session after a restart, when it matches the session owner and transcript;
- forks the stored CLI session, when the session owner changed, such as after a Steiner Fork;
- reseeds a CLI session from the Steiner transcript, when the restored stored state is missing, dead, mismatched, or was built for a different system prompt or tool set. A session that is still live is not reseeded for a changed prompt or tool set.

A reseed sends the non-system history as one transcript message. Prompt cache reuse is not expected for a reseeded turn. Images still held in memory are included. Image data stripped after it was consumed cannot return. Earlier tool calls appear as text.

## Privacy and telemetry

Prompts, system instructions, images that are accepted by the provider, tool calls, and tool results are sent to the local `claude` process. The process then handles the network request under the signed-in Claude account. Claude's own data handling, telemetry, retention, and account controls apply; see Anthropic's legal and compliance documentation linked above.

Steiner does not read the Claude credential file or copy its token into provider configuration. It checks `claude auth status`, starts the CLI, and removes the credential and CLI entrypoint variables from the child environment. Steiner's own logging and diagnostics settings still apply. Logs and diagnostics can contain prompts or tool output when enabled, so review `logging` and `diagnostics` settings before using this provider.

## Model selection

Model discovery uses the signed-in CLI's `initialize` control response and lists the full resolved model IDs available to that account. If discovery fails or returns no usable models, Steiner uses a static fallback catalog so the provider remains selectable. See [Model enumeration](model-enumeration.md#claude-subscription) for the timeout and fallback behavior.

You can select a discovered model with `/model`, or use a raw reference such as `claude/<full-model-id>`. The provider can switch model and reasoning effort during a live session. The model switch is sent to the existing CLI process, not implemented by adding a model name to the pool key.

## Limitations

- Previously sent history is not rewritten in the CLI. Changes to sent messages, other than removal of consumed image data, are not patched into the live session. Steiner reseeds instead, as described in Session continuity. In an interactive conversation, `/clear` starts a new empty Steiner and Claude CLI session; it does not rewrite the prior transcript. There is no user-facing rewind.
- Support for changing already-sent history is tracked in [#895](https://github.com/luispabon/steiner/issues/895).
- The CLI owns its process identity and transcript. Its identity or reminders can change as the CLI changes, and switching models can reset model-specific cache state. Do not treat those details as stable Steiner storage.
- The provider is not a replacement for the native `anthropic` API provider. It uses the signed-in CLI and the subscription's account controls.
- Claude CLI and Anthropic can change these behaviors without a corresponding Steiner release. Recheck the compliance links and CLI version when an account or model stops working.
