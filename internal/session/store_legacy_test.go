package session

import (
	"bytes"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

// Not parallel: the test swaps the process-wide default slog logger.
func TestRestoreLegacyLineageCorruptDataLogsWarning(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	session := Session{ID: "legacy-id", Title: "kept"}
	got := restoreLegacyLineage(session, []byte(`{"id":"legacy-id","conversation":"not-a-list"}`))
	if !reflect.DeepEqual(got, session) {
		t.Fatalf("restoreLegacyLineage() = %+v, want session unchanged %+v", got, session)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "session_id=legacy-id") {
		t.Fatalf("logs = %q, want WARN record with session_id=legacy-id", logs.String())
	}
}
