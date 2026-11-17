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

## API retries

The CLI retries its own retryable API failures, using the limit Claude Code sets, before it produces any output. While a streaming response does so, the activity row shows `retrying N/M` with the attempt and the permitted retry count. A non-streaming response has no live indicator, so it reports one summary warning afterwards naming how many retries happened and the last failure. A retry discards nothing Steiner already received and does not end the response. The dev log records the failure category, the HTTP status when the CLI had a response, and the delay before the next attempt.

When the CLI has already retried an API error in a turn, Steiner does not start a fresh process to retry it again.

## Failed responses

If a response fails before anything was shown and Steiner can recover it, the activity row reports `retrying 1/2` and the request is sent again on a replacement CLI process. It resumes your saved CLI session where it can, so the prompt cache can stay warm. That happens at most twice per request.

Once part of a reply has appeared, including thinking, Steiner does not restart or continue it automatically. A failure then shows the error and keeps the text you already saw as untrusted incomplete assistant text. Your next message resumes the saved CLI session at the last completed step and passes that text to the model as a quoted note, so the conversation carries on with the prompt cache able to stay warm instead of replaying everything flattened.

Automatic continuation of a cut-off reply was tested against the live CLI and rejected: in most trials the model dropped a line break where the reply had been cut, so the joined text was not reliable. A response that a usage limit, an auth or billing refusal, or a stop of yours ended is not recovered either: those need your action.

## Privacy

- Prompts, system instructions, accepted images, tool calls, and tool results go to the local `claude` process, which sends them to Anthropic under your Claude account. Claude's own data handling, telemetry, retention, and account controls apply.
- The CLI adds some context Steiner cannot turn off, including the signed-in account's email address and environment details such as the working directory, platform, and date. Steiner turns off the CLI's auto-memory, `CLAUDE.md` loading, and git instructions.
- Steiner does not read the Claude credential file or copy its token into Steiner configuration. It removes `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, and `CLAUDE_CODE_OAUTH_TOKEN` from the CLI's environment.
- The CLI's built-in tools are disabled. The model can use only the tools Steiner publishes, and Steiner's approval and sandbox policies apply to them.
- Steiner's own logs and diagnostics can contain prompts and tool output when enabled. Review the [`logging` block](configuration.md#logging-block) and [`diagnostics` block](configuration.md#diagnostics-block) before using this provider.

## Limitations

- Oneshot runs refuse this provider. Assign another provider to oneshot roles; see [Oneshot](oneshot.md).
- Steiner cannot rewrite history the CLI has already received. When the CLI session cannot continue from Steiner's conversation, for example after a changed system prompt or tool set, or a request the stored session cannot extend, Steiner starts a new CLI session and sends the earlier conversation as one transcript message, and warns you that prior context was replayed flattened. Prompt cache reuse is not expected for that turn, and earlier tool calls appear as text.
- Stopping a response does not throw the CLI session away. When the CLI confirms it stopped the turn, the next request continues on the same process, which keeps its prompt cache warm and produces no warning. When the process cannot be used (it exited, the stop could not be confirmed, or Steiner was restarted), the next request resumes the saved CLI session at its last completed step instead of replaying the conversation. A resumed request that carries the text you saw before stopping sends it to the model as a quoted note labelled untrusted and possibly incomplete, marked off so it cannot be mistaken for a complete answer; the note keeps the first and last parts of a very long cut response. A restart before that request resumes without any warning.
- A response you interrupted, or that a usage limit or error ended part-way, is kept as untrusted incomplete assistant text. A resumed request quotes it in the stopped-reply note, and a flattened transcript labels it as an interrupted partial, so it is not mistaken for a complete answer.
- Interrupting a response keeps the CLI process when the CLI confirms that it stopped the turn, so the next request reuses the warm process and its prompt cache. Confirming that can take up to 5 seconds, though it usually takes tens of milliseconds. When Steiner cannot confirm that, the process is discarded instead, and the request that continues the conversation warns you that it was discarded. A usage limit that stops a response part-way discards the process. The warning is kept in memory only: if Steiner is restarted before that request, you are not told. A conversation summary never reports it, and a request that fails keeps the warning for the next one instead of losing it. A response that fails after the warning was already shown also keeps it, so the next request may show the same warning again. When the same request also replays the conversation as one flattened message, you get one warning covering both, not two.
- If the CLI starts a second response after the first one ended in the same turn, possibly after its safety safeguards refused the request, Steiner stops that response and reports it.
- This provider does not replace the `anthropic` API provider. It depends on the CLI and on your subscription's account controls.
- The CLI and Anthropic can change behaviour without a Steiner release. When an account or model stops working, check the CLI version and the compliance links above.
