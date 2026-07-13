package main

import (
	"os"
	"runtime"

	"github.com/luispabon/steiner/internal/provider"
)

var version = "dev"
var commit = "none"
var buildDate = "unknown"
var goVersion = runtime.Version()

var newScheduler = provider.NewScheduler
var newOpenAICompat = func(cfg provider.ClientConfig) (provider.Provider, error) {
	return provider.NewOpenAICompat(cfg)
}
var newAnthropic = func(cfg provider.ClientConfig) (provider.Provider, error) {
	return provider.NewAnthropic(cfg)
}
var newCodexResponses = func(cfg provider.ClientConfig) (provider.Provider, error) {
	return provider.NewCodexResponses(cfg)
}

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
