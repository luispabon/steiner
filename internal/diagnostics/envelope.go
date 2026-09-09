// Package diagnostics writes structured, config-gated instrumentation records
// to per-stream JSONL files. It imports stdlib only so provider, delegation,
// tool, agent and usagestats can all depend on it without import cycles; the
// composition root translates configuration into Options.
package diagnostics

import "time"

// Kind identifies which diagnostics stream a record belongs to. Each kind gets
// its own file so a cheap stream stays cheap to parse.
type Kind string

const (
	// KindCache records prompt-cache observations.
	KindCache Kind = "cache"
	// KindProvider records one entry per model call, whatever the outcome.
	KindProvider Kind = "provider"
	// KindTool records tool execution and delegation lifecycle entries.
	KindTool Kind = "tool"
)

// Kinds lists every stream in a stable order.
func Kinds() []Kind {
	return []Kind{KindCache, KindProvider, KindTool}
}

// fileName is the stream's file name inside the diagnostics directory.
func (k Kind) fileName() string {
	return string(k) + ".jsonl"
}

// Source identifies which call surface produced a record. It mirrors
// usagestats.Source, which converts into this type rather than the reverse:
// this package must not import another steiner package.
type Source string

const (
	// SourceParent is the top-level orchestrator run.
	SourceParent Source = "parent"
	// SourceSubAgent is a delegated sub-agent run.
	SourceSubAgent Source = "sub_agent"
	// SourceAdvisor is the advisor tool.
	SourceAdvisor Source = "advisor"
)

// Record is one diagnostics event. Payload holds kind-specific fields and must
// contain only fixed-size scalars; large content belongs behind CaptureBodies.
type Record struct {
	Timestamp time.Time `json:"ts"`
	RunID     string    `json:"run_id,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	BuildSHA  string    `json:"build_sha,omitempty"`
	Dirty     bool      `json:"dirty,omitempty"`
	Kind      Kind      `json:"kind"`
	Source    Source    `json:"source,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`
	AgentType string    `json:"agent_type,omitempty"`
	Turn      int       `json:"turn,omitempty"`
	Seq       int64     `json:"seq"`
	Payload   any       `json:"payload,omitempty"`
}
