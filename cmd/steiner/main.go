package main

import (
	"os"
	"runtime"
	"strings"

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
	if err := executeRootCommand(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}

func executeRootCommand(args []string) error {
	cmd := newRootCommand()
	cmd.SetArgs(normalizeRootResumeArgs(args))
	return cmd.Execute()
}

func normalizeRootResumeArgs(args []string) []string {
	normalized := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || arg == "oneshot" {
			normalized = append(normalized, args[i:]...)
			break
		}
		if arg == "--resume" && i+1 < len(args) && args[i+1] != "--" && !strings.HasPrefix(args[i+1], "-") {
			normalized = append(normalized, "--resume="+args[i+1])
			i++
			continue
		}
		normalized = append(normalized, arg)
	}
	return normalized
}
