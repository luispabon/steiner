package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

var tokenizerCache sync.Map

func EstimateChatRequestTokens(ctx context.Context, request ChatRequest) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	wire, err := chatRequestWire(request, request.Model, request.Stream)
	if err != nil {
		return 0, err
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return 0, fmt.Errorf("marshal request payload: %w", err)
	}
	enc, err := tokenizerForModel(wire.Model)
	if err != nil {
		return 0, err
	}
	count, err := enc.Count(string(payload))
	if err != nil {
		return 0, fmt.Errorf("count request payload tokens: %w", err)
	}
	return count, nil
}

func UsageTokenCount(usage *UsageStats) int {
	return normalizedTokenCount(usage)
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
