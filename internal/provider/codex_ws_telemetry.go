package provider

import (
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/usagestats"
)

// WebSocket connection lifecycle events written to the telemetry file. They
// share the file with internal/usagestats' usage lines so that a reconnect can
// be aligned against the cache-read collapse it causes on the following turn.
const (
	wsTelemetryEventDial      = "dial"
	wsTelemetryEventReconnect = "reconnect"
)

// wsTelemetryLine is one WebSocket connection event in the JSONL telemetry file.
type wsTelemetryLine struct {
	Kind      string `json:"kind"`
	Timestamp string `json:"ts"`
	RunID     string `json:"run_id,omitempty"`
	Event     string `json:"event"`
	Reason    string `json:"reason,omitempty"`
	CacheKey  string `json:"cache_key,omitempty"`
}

// wsTelemetryState holds the currently open telemetry file. The path is
// re-read from the environment on every call and the file reopened when it
// changes, so the writer needs no process-lifetime initialisation and stays
// testable in-process.
var wsTelemetryState struct {
	mu    sync.Mutex
	path  string
	file  *os.File
	runID string
}

// recordWSTelemetry appends one connection event to the telemetry file named by
// usagestats.TelemetryEnvVar. It is a no-op when that variable is unset, and
// never fails a request: telemetry is diagnostic only.
func recordWSTelemetry(event, reason, cacheKey string) {
	path := os.Getenv(usagestats.TelemetryEnvVar)
	if path == "" {
		return
	}

	wsTelemetryState.mu.Lock()
	defer wsTelemetryState.mu.Unlock()

	if wsTelemetryState.path != path || wsTelemetryState.file == nil {
		if wsTelemetryState.file != nil {
			_ = wsTelemetryState.file.Close()
			wsTelemetryState.file = nil
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			slog.Warn("open ws telemetry file", "path", path, "error", err)
			wsTelemetryState.path = ""
			return
		}
		wsTelemetryState.path = path
		wsTelemetryState.file = f
	}
	wsTelemetryState.runID = os.Getenv(usagestats.TelemetryRunEnvVar)

	encoded, err := json.Marshal(wsTelemetryLine{
		Kind:      "ws",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		RunID:     wsTelemetryState.runID,
		Event:     event,
		Reason:    reason,
		CacheKey:  cacheKey,
	})
	if err != nil {
		slog.Warn("marshal ws telemetry line", "error", err)
		return
	}
	if _, err := wsTelemetryState.file.Write(append(encoded, '\n')); err != nil {
		slog.Warn("write ws telemetry line", "error", err)
	}
}
