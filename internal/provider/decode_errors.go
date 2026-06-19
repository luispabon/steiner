package provider

import "errors"

var (
	errDecodeChatCompletionResponse = errors.New("decode chat completion response")
	errDecodeStreamChunk            = errors.New("decode stream chunk")
	errDecodeStreamChunkUnexpected  = errors.New("decode stream chunk unexpected end of JSON input")
	errDecodeToolCallArguments      = errors.New("decode tool call arguments")
)
