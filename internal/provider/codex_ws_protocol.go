package provider

// Codex WebSocket protocol constants, sourced from research.md and empirical
// probe testing. These are unconfirmed until step 1's probe runs.

// WSEndpointURL is the WebSocket endpoint for Codex Responses.
// Unconfirmed, from research.md.
const WSEndpointURL = "wss://chatgpt.com/backend-api/codex/responses"

// WSBetaHeaderValue is the OpenAI-Beta header value for the WebSocket protocol.
// Unconfirmed, from research.md.
const WSBetaHeaderValue = "responses_websockets=2026-02-06"

// WSHeaderInstallationID is the header name for installation ID.
// Unconfirmed, from research.md.
const WSHeaderInstallationID = "x-codex-installation-id"

// WSHeaderClientRequestID is the header name for client request ID (thread ID).
// Unconfirmed, from research.md.
const WSHeaderClientRequestID = "x-client-request-id"

// WSEventTypeMetadata is the Codex event carrying per-response metadata,
// confirmed by live probe 2026-08-24. The other event types the stream emits
// are matched by string literal in processResponsesStreamEvent
// (codex_responses_stream.go), so they are not duplicated as constants here.
const WSEventTypeMetadata = "codex.response.metadata"

// WSReadLimitBytes is the maximum message size for WebSocket reads.
// The coder/websocket library defaults to 32KB, which is insufficient because even short
// user messages can trigger responses that include echoed request context (system instructions,
// tool schemas), routinely exceeding 32KB. This larger limit (64MB) accommodates large responses
// while remaining bounded against malformed streams.
const WSReadLimitBytes int64 = 64 * 1024 * 1024
