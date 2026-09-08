package tool

import "context"

// MutateDiagnosticsFunc queries LSP diagnostics for files a mutate call
// touched and returns a pre-rendered, bounded report section (empty string
// if there's nothing to report). Implementations must be best-effort: skip
// files with no server already running and ready, and must not spawn one.
type MutateDiagnosticsFunc func(ctx context.Context, files []string) string
