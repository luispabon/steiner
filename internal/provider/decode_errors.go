package provider

import "errors"

var (
	errDecodeChatCompletionResponse = errors.New("decode chat completion response")
	errDecodeStreamChunk            = errors.New("decode stream chunk")
	errDecodeStreamChunkUnexpected  = errors.New("decode stream chunk unexpected end of JSON input")
	errDecodeToolCallArguments      = errors.New("decode tool call arguments")
	// errResponsesStreamFailed marks a terminal response.failed event. The
	// server rejected the response itself, so resending would fail the same way.
	errResponsesStreamFailed = errors.New("responses stream failed")
	// errResponsesStreamFailedTransient marks a response.failed event whose
	// error code signals a transient server condition worth another attempt.
	errResponsesStreamFailedTransient = errors.New("responses stream failed transiently")
)
