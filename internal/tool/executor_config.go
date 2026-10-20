package tool

import (
	"slices"

	"github.com/luispabon/steiner/internal/config"
)

// defaultToolOutputMaxBytes is the output cap used when no positive
// tool_output_max_bytes is configured.
const defaultToolOutputMaxBytes = 65536

// ExecutorConfig is the configuration an Executor consumes: the tool output byte
// cap and the path policy. Build it with ExecutorConfigFromConfig and share the
// same value between a parent and its children. Fields are unexported so the
// value is immutable at the seam. The zero value applies the default output cap
// and an empty path policy.
type ExecutorConfig struct {
	outputLimit int
	paths       config.PathsConfig
}

// ExecutorConfigFromConfig projects limits.tool_output_max_bytes and paths from
// cfg into an ExecutorConfig. A non-positive tool_output_max_bytes falls back to
// 65536. Path slices are cloned so the result does not share backing arrays with
// cfg.
func ExecutorConfigFromConfig(cfg config.Config) ExecutorConfig {
	outputLimit := cfg.Limits.ToolOutputMaxBytes
	if outputLimit < 1 {
		outputLimit = defaultToolOutputMaxBytes
	}
	paths := cfg.Paths
	paths.WritablePaths = slices.Clone(paths.WritablePaths)
	paths.BlockedPaths = slices.Clone(paths.BlockedPaths)
	paths.ExcludePaths = slices.Clone(paths.ExcludePaths)
	paths.ExcludePatterns = slices.Clone(paths.ExcludePatterns)
	return ExecutorConfig{outputLimit: outputLimit, paths: paths}
}
