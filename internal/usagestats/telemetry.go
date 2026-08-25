package usagestats

import (
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// TelemetryEnvVar names the environment variable holding a JSONL path. When
// set, every recorded Observation is appended to that file as one JSON line,
// alongside the Codex WebSocket connection events written by internal/provider
// to the same file. This exists to make per-turn cache behaviour observable in
// headless runs, where the TUI's /cache-stats view is unavailable and the
// persisted store only keeps hour-bucketed aggregates.
const TelemetryEnvVar = "STEINER_USAGE_TELEMETRY"

// TelemetryRunEnvVar names the environment variable holding a run identifier
// stamped onto every telemetry line, so lines from separate runs sharing one
// file can be told apart.
const TelemetryRunEnvVar = "STEINER_USAGE_TELEMETRY_RUN"

// telemetryLine is one usage record in the JSONL telemetry file.
type telemetryLine struct {
	Kind              string `json:"kind"`
	Timestamp         string `json:"ts"`
	RunID             string `json:"run_id,omitempty"`
	Seq               int64  `json:"seq"`
	Source            string `json:"source"`
	ProviderAlias     string `json:"provider_alias,omitempty"`
	ProviderType      string `json:"provider_type,omitempty"`
	BackendModelID    string `json:"backend_model_id,omitempty"`
	PromptTokens      int    `json:"prompt_tokens"`
	CacheReadTokens   int    `json:"cache_read_tokens"`
	CacheCreateTokens int    `json:"cache_create_tokens"`
	CompletionTokens  int    `json:"completion_tokens"`
}

// telemetry appends JSON lines to a file. A nil *telemetry writes nothing, so
// callers need no enabled check.
type telemetry struct {
	mu    sync.Mutex
	file  *os.File
	runID string
	seq   atomic.Int64
}

// newTelemetryFromEnv returns a telemetry writer when TelemetryEnvVar names a
// writable path, and nil otherwise. A path that cannot be opened is reported
// and downgraded to nil rather than failing the run, since telemetry is
// diagnostic and must never take a session down.
func newTelemetryFromEnv() *telemetry {
	path := os.Getenv(TelemetryEnvVar)
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Warn("open usage telemetry file", "path", path, "error", err)
		return nil
	}
	return &telemetry{file: f, runID: os.Getenv(TelemetryRunEnvVar)}
}

// record appends one observation. Safe on a nil receiver.
func (t *telemetry) record(obs Observation, at time.Time) {
	if t == nil {
		return
	}
	line := telemetryLine{
		Kind:              "usage",
		Timestamp:         at.UTC().Format(time.RFC3339Nano),
		RunID:             t.runID,
		Seq:               t.seq.Add(1),
		Source:            sourceName(obs.Source),
		ProviderAlias:     obs.ProviderAlias,
		ProviderType:      obs.ProviderType,
		BackendModelID:    obs.BackendModelID,
		PromptTokens:      obs.PromptTokens,
		CacheReadTokens:   obs.CacheReadTokens,
		CacheCreateTokens: obs.CacheCreateTokens,
		CompletionTokens:  obs.CompletionTokens,
	}
	encoded, err := json.Marshal(line)
	if err != nil {
		slog.Warn("marshal usage telemetry line", "error", err)
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.file.Write(append(encoded, '\n')); err != nil {
		slog.Warn("write usage telemetry line", "error", err)
	}
}

func sourceName(s Source) string {
	switch s {
	case SourceSubAgent:
		return "sub_agent"
	case SourceAdvisor:
		return "advisor"
	case SourceParent:
		return "parent"
	default:
		return "unknown"
	}
}
