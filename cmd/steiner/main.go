package main

import (
	"context"
	"fmt"
	"os"

	"github.com/luispabon/steiner/internal/provider"
)

var version = "dev"

var newScheduler = provider.NewScheduler
var newOpenAICompat = func(cfg provider.OpenAICompatConfig) (provider.Provider, error) {
	return provider.NewOpenAICompat(cfg)
}
var newAnthropic = func(cfg provider.OpenAICompatConfig) (provider.Provider, error) {
	return &anthropicStubProvider{}, nil
}

type anthropicStubProvider struct{}

func (p *anthropicStubProvider) ChatCompletion(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, fmt.Errorf("anthropic provider is not implemented")
}

func (p *anthropicStubProvider) StreamChatCompletion(_ context.Context, _ provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, fmt.Errorf("anthropic provider is not implemented")
}

func (p *anthropicStubProvider) SupportsUsageStats() bool {
	return false
}

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
