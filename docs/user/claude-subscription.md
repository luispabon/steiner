# Claude subscription

The `claude_subscription` provider type runs your signed-in `claude` CLI and uses the Claude subscription attached to that login. It is an unofficial Steiner integration and is not affiliated with Anthropic.

Anthropic can change the CLI, subscription terms, or supported behaviour at any time. Before using this provider, read Anthropic's current guidance:

- [Use the Claude Agent SDK with your Claude plan](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan)
- [Claude legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)

## Requirements

- Claude Code installed, with the `claude` command on `PATH`.
- Claude CLI version `2.1.294` or newer. An older CLI is refused with a prompt to run `claude update`.
- A Claude subscription login through claude.ai. Steiner checks `claude auth status` and accepts only a claude.ai, first-party login. API-key, Bedrock, Vertex, and Foundry logins are refused; use the `anthropic` provider with API credentials for those.

## Setup

1. Sign in with the account whose subscription should be used:

   ```bash
   claude auth login --claudeai
   ```

2. Add a provider with `type: claude_subscription`. It takes no `base_url`, `api_key`, `api_key_env`, `headers`, or `timeout`; config validation rejects them.

   ```yaml
   providers:
     claude:
       type: claude_subscription

   models:
     profiles:
       default:
         default_model: claude/<full-model-id>
   ```

3. Use a full model ID reported by the signed-in CLI, not a display name or another provider's short alias. Start `steiner` and open `/model` to see the IDs available to your account.

For model references, profiles, and the model picker, see [Provider and model setup](provider-and-model-setup.md).

If the CLI reports that it is signed out, Steiner tells you to run `claude auth login --claudeai` in a terminal. After signing in, retry the request yourself; Steiner does not replay a refused request.

## Extra usage safety gate

Claude accounts can turn on extra usage (also called usage credits), which bills usage beyond the subscription. Steiner refuses to use an account that could incur those charges:

- Before each turn, Steiner checks the account's usage state through the CLI. The turn goes ahead only when the CLI confirms that extra usage is turned off.
- If extra usage is turned on, Steiner refuses the turn at once.
- If Steiner cannot confirm the state, it waits briefly and checks again, then refuses (fails closed). The error names what could not be confirmed.
- If the CLI reports during a turn that paid extra usage has started, Steiner stops the response.

To use this provider, turn off extra usage in Claude at **Settings -> Usage**, then retry. The ordinary subscription limit is separate: when you reach it, the turn stops and Steiner reports when the limit resets. Steiner turns off the CLI's unattended retry mode, so the limit stops the turn instead of waiting for the window to reset.

## Model discovery

Model discovery asks the signed-in CLI for the models your account can use, without making a model call. If the CLI is missing, signed out, or returns no usable models within 20 seconds, the picker shows a built-in fallback list so the provider stays selectable. See [Provider and model setup](provider-and-model-setup.md) for discovery settings and cache commands.

You can switch model and reasoning effort during a session.

## Privacy

- Prompts, system instructions, accepted images, tool calls, and tool results go to the local `claude` process, which sends them to Anthropic under your Claude account. Claude's own data handling, telemetry, retention, and account controls apply.
- The CLI adds some context Steiner cannot turn off, including the signed-in account's email address and environment details such as the working directory, platform, and date. Steiner turns off the CLI's auto-memory, `CLAUDE.md` loading, and git instructions.
- Steiner does not read the Claude credential file or copy its token into Steiner configuration. It removes `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, and `CLAUDE_CODE_OAUTH_TOKEN` from the CLI's environment.
- The CLI's built-in tools are disabled. The model can use only the tools Steiner publishes, and Steiner's approval and sandbox policies apply to them.
- Steiner's own logs and diagnostics can contain prompts and tool output when enabled. Review the [`logging` block](configuration.md#logging-block) and [`diagnostics` block](configuration.md#diagnostics-block) before using this provider.

## Limitations

- Oneshot runs refuse this provider. Assign another provider to oneshot roles; see [Oneshot](oneshot.md).
- Steiner cannot rewrite history the CLI has already received. When the CLI session cannot continue from Steiner's conversation, for example after a restart or a changed system prompt or tool set, Steiner starts a new CLI session and sends the earlier conversation as one transcript message. Prompt cache reuse is not expected for that turn, and earlier tool calls appear as text.
- If the CLI starts a second response after the first one ended in the same turn, possibly after its safety safeguards refused the request, Steiner stops that response and reports it.
- This provider does not replace the `anthropic` API provider. It depends on the CLI and on your subscription's account controls.
- The CLI and Anthropic can change behaviour without a Steiner release. When an account or model stops working, check the CLI version and the compliance links above.
