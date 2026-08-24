package provider

// Codex WebSocket protocol constants, sourced from research.md and empirical
// probe testing. These are unconfirmed until step 1's probe runs.

// WSEndpointURL is the WebSocket endpoint for Codex Responses.
// Unconfirmed, from research.md.
const WSEndpointURL = "wss://chatgpt.com/backend-api/codex/responses"

// WSBetaHeaderValue is the OpenAI-Beta header value for the WebSocket protocol.
// Unconfirmed, from research.md.
const WSBetaHeaderValue = "responses_websockets=2026-02-06"

// WSHeaderTurnState is the header name for turn-state token exchange.
// Unconfirmed, from research.md.
const WSHeaderTurnState = "x-codex-turn-state"

// WSHeaderInstallationID is the header name for installation ID.
// Unconfirmed, from research.md.
const WSHeaderInstallationID = "x-codex-installation-id"

// WSHeaderRoutingHint is the header name for routing hints.
// Unconfirmed, from research.md.
const WSHeaderRoutingHint = "x-codex-routing-hint"

// WSHeaderClientRequestID is the header name for client request ID (thread ID).
// Unconfirmed, from research.md.
const WSHeaderClientRequestID = "x-client-request-id"

// WebSocket event type constants. These match the event types already handled
// by processResponsesStreamEvent in codex_responses_stream.go.
// Most are unconfirmed from research.md; WSEventTypeMetadata is confirmed by
// live probe 2026-08-24.
// Note: Real event types also observed but not yet in this constant set:
// response.in_progress, response.content_part.added, response.content_part.done,
// codex.rate_limits, responsesapi.websocket_timing. These are informational for
// future reference if additional constants are needed.
const (
	WSEventTypeResponseCreated           = "response.created"
	WSEventTypeOutputItemAdded           = "response.output_item.added"
	WSEventTypeOutputTextDelta           = "response.output_text.delta"
	WSEventTypeReasoningTextDelta        = "response.reasoning_text.delta"
	WSEventTypeReasoningSummaryTextDelta = "response.reasoning_summary_text.delta"
	WSEventTypeOutputItemDone            = "response.output_item.done"
	WSEventTypeMetadata                  = "codex.response.metadata"
	WSEventTypeCompleted                 = "response.completed"
)

// WSCloseCodePolicy is the WebSocket close code for policy violations.
// Servers use this to indicate rate limiting, protocol breaches, and similar
// backend-side rejection scenarios.
// Unconfirmed, from research.md.
const WSCloseCodePolicy = 1008
