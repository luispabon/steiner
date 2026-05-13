package provider

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

var tokenizerCache sync.Map

const (
	requestOverheadTokens   = 8
	messageOverheadTokens   = 4
	toolCallOverheadTokens  = 6
	toolSpecOverheadTokens  = 6
	mapEntryOverheadTokens  = 2
	listEntryOverheadTokens = 1
)

// EstimateMessageTokens estimates the semantic token cost of a message.
func EstimateMessageTokens(ctx context.Context, model string, message Message) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	enc, err := tokenizerForModel(model)
	if err != nil {
		return 0, err
	}
	estimator := semanticTokenEstimator{
		ctx: ctx,
		enc: enc,
	}
	total := estimator.countMessage(message)
	if estimator.err != nil {
		return 0, estimator.err
	}
	return total, nil
}

// EstimateToolSpecTokens estimates the semantic token cost of a tool schema.
func EstimateToolSpecTokens(ctx context.Context, model string, tool ToolSpec) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	enc, err := tokenizerForModel(model)
	if err != nil {
		return 0, err
	}
	estimator := semanticTokenEstimator{
		ctx: ctx,
		enc: enc,
	}
	total := estimator.countToolSpec(tool)
	if estimator.err != nil {
		return 0, estimator.err
	}
	return total, nil
}

// RequestOverheadTokens returns the fixed token overhead for a request envelope.
func RequestOverheadTokens() int {
	return requestOverheadTokens
}

// EstimateChatRequestTokens estimates the semantic token cost of a request.
func EstimateChatRequestTokens(ctx context.Context, request ChatRequest) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	enc, err := tokenizerForModel(request.Model)
	if err != nil {
		return 0, err
	}
	estimator := semanticTokenEstimator{
		ctx: ctx,
		enc: enc,
	}
	return estimator.countRequest(request)
}

func UsageTokenCount(usage *UsageStats) int {
	return normalizedTokenCount(usage)
}

type semanticTokenEstimator struct {
	ctx context.Context
	enc tokenizer.Codec
	err error
}

func (e *semanticTokenEstimator) countRequest(request ChatRequest) (int, error) {
	total := requestOverheadTokens
	for _, tool := range request.Tools {
		total += e.countToolSpec(tool)
	}
	for _, message := range request.Messages {
		total += e.countMessage(message)
	}
	if e.err != nil {
		return 0, e.err
	}
	return total, nil
}

func (e *semanticTokenEstimator) countMessage(message Message) int {
	total := messageOverheadTokens
	total += e.countText(string(message.Role))
	total += e.countText(message.Name)
	total += e.countText(message.ToolCallID)
	total += e.countText(message.Content)
	for _, call := range message.ToolCalls {
		total += e.countToolCall(call)
	}
	return total
}

func (e *semanticTokenEstimator) countToolCall(call ToolCall) int {
	total := toolCallOverheadTokens
	total += e.countText(call.ID)
	total += e.countText(call.Name)
	total += e.countValue(call.Arguments)
	return total
}

func (e *semanticTokenEstimator) countToolSpec(tool ToolSpec) int {
	total := toolSpecOverheadTokens
	total += e.countText(tool.Type)
	total += e.countText(tool.Function.Name)
	total += e.countText(tool.Function.Description)
	total += e.countValue(tool.Function.Parameters)
	return total
}

func (e *semanticTokenEstimator) countText(text string) int {
	if text == "" || e.err != nil {
		return 0
	}
	if err := e.ctx.Err(); err != nil {
		e.err = err
		return 0
	}
	count, err := e.enc.Count(text)
	if err != nil {
		e.err = fmt.Errorf("count semantic text tokens: %w", err)
		return 0
	}
	return count
}

func (e *semanticTokenEstimator) countValue(value any) int {
	if e.err != nil {
		return 0
	}
	if err := e.ctx.Err(); err != nil {
		e.err = err
		return 0
	}
	if value == nil {
		return 0
	}

	visited := reflect.ValueOf(value)
	if !visited.IsValid() {
		return 0
	}

	switch visited.Kind() {
	case reflect.Interface, reflect.Pointer:
		if visited.IsNil() {
			return 0
		}
		return e.countValue(visited.Elem().Interface())
	case reflect.String:
		return e.countText(visited.String())
	case reflect.Bool:
		if visited.Bool() {
			return e.countText("true")
		}
		return e.countText("false")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return e.countText(strconv.FormatInt(visited.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return e.countText(strconv.FormatUint(visited.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		return e.countText(strconv.FormatFloat(visited.Float(), 'f', -1, 64))
	case reflect.Slice, reflect.Array:
		total := visited.Len() * listEntryOverheadTokens
		for i := 0; i < visited.Len(); i++ {
			total += e.countValue(visited.Index(i).Interface())
		}
		return total
	case reflect.Map:
		total := 0
		for _, key := range visited.MapKeys() {
			total += mapEntryOverheadTokens
			total += e.countText(fmt.Sprint(key.Interface()))
			total += e.countValue(visited.MapIndex(key).Interface())
		}
		return total
	default:
		if stringer, ok := value.(fmt.Stringer); ok {
			return e.countText(stringer.String())
		}
		return e.countText(fmt.Sprint(value))
	}
}

func tokenizerForModel(model string) (tokenizer.Codec, error) {
	key := strings.TrimSpace(model)
	if key == "" {
		key = string(tokenizer.Cl100kBase)
	}
	if cached, ok := tokenizerCache.Load(key); ok {
		if enc, ok := cached.(tokenizer.Codec); ok {
			return enc, nil
		}
	}
	if key != "" {
		if enc, err := tokenizer.ForModel(tokenizer.Model(key)); err == nil {
			tokenizerCache.Store(key, enc)
			return enc, nil
		}
	}
	enc, err := tokenizer.Get(encodingNameForModel(model))
	if err != nil {
		return nil, fmt.Errorf("load tokenizer %s: %w", key, err)
	}
	tokenizerCache.Store(key, enc)
	return enc, nil
}

func encodingNameForModel(model string) tokenizer.Encoding {
	switch {
	case strings.HasPrefix(model, "gpt-4.5"),
		strings.HasPrefix(model, "gpt-4.1"),
		strings.HasPrefix(model, "gpt-4o"),
		strings.HasPrefix(model, "o1"),
		strings.HasPrefix(model, "o3"):
		return tokenizer.O200kBase
	case strings.HasPrefix(model, "gpt-4"),
		strings.HasPrefix(model, "gpt-3.5"),
		strings.HasPrefix(model, "text-embedding-ada-002"),
		strings.HasPrefix(model, "text-embedding-3"):
		return tokenizer.Cl100kBase
	case strings.HasPrefix(model, "text-davinci"),
		strings.HasPrefix(model, "code-davinci"),
		strings.HasPrefix(model, "code-cushman"):
		return tokenizer.P50kBase
	case strings.HasPrefix(model, "davinci"),
		strings.HasPrefix(model, "curie"),
		strings.HasPrefix(model, "babbage"),
		strings.HasPrefix(model, "ada"),
		model == "gpt2":
		return tokenizer.R50kBase
	default:
		return tokenizer.Cl100kBase
	}
}
