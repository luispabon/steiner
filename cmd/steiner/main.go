package main

import (
	"os"

	"github.com/luispabon/steiner/internal/provider"
)

var version = "dev"

var newScheduler = provider.NewScheduler
var newOpenAICompat = func(cfg provider.OpenAICompatConfig) (provider.Provider, error) {
	return provider.NewOpenAICompat(cfg)
}
var newAnthropic = func(cfg provider.OpenAICompatConfig) (provider.Provider, error) {
	return provider.NewAnthropic(cfg)
}

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
