package diagnostics

// Streams gates each diagnostics stream independently; the cost per stream
// differs by orders of magnitude.
type Streams struct {
	Cache    bool
	Provider bool
	Tool     bool
}

// enabled reports whether kind's stream is on.
func (s Streams) enabled(kind Kind) bool {
	switch kind {
	case KindCache:
		return s.Cache
	case KindProvider:
		return s.Provider
	case KindTool:
		return s.Tool
	default:
		return false
	}
}

// any reports whether at least one stream is on.
func (s Streams) any() bool {
	return s.Cache || s.Provider || s.Tool
}

// Options configures a Writer. The composition root fills this in from
// configuration; Dir is always supplied by the caller so no test can be
// pointed at real user state by overriding a package variable.
type Options struct {
	// Dir is the directory holding the per-stream JSONL files. Required.
	Dir string
	// RetentionDays drops records older than this many days when the writer
	// opens. Values below one disable retention pruning.
	RetentionDays int
	// Streams gates the individual streams.
	Streams Streams
	// CaptureBodies allows record payloads to carry full message, tool and
	// block content instead of bounded scalars.
	CaptureBodies bool
	// RunID separates records written by concurrent processes sharing Dir.
	RunID string
	// BuildSHA and Dirty identify the running binary, so a before/after
	// comparison can be scoped to a build rather than to a time window.
	BuildSHA string
	Dirty    bool
}
