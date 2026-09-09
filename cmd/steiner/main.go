package main

import (
	"os"
	"runtime"

	"github.com/luispabon/steiner/internal/provider"
)

var version = "dev"
var commit = "none"

// dirty is set by the build (-X main.dirty) to "true" when the working tree
// had uncommitted changes. It is a string because -X can only set strings;
// buildDirty() converts it.
var dirty = ""
var buildDate = "unknown"
var goVersion = runtime.Version()

var newOpenAICompat = func(cfg provider.ClientConfig) (provider.Provider, error) {
	return provider.NewOpenAICompat(cfg)
}
var newAnthropic = func(cfg provider.ClientConfig) (provider.Provider, error) {
	return provider.NewAnthropic(cfg)
}
var newCodexResponses = func(cfg provider.ClientConfig) (provider.Provider, error) {
	return provider.NewCodexResponses(cfg)
}
var newCodexResponsesWS = func(cfg provider.ClientConfig) (provider.Provider, error) {
	return provider.NewCodexResponsesWS(cfg)
}

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
