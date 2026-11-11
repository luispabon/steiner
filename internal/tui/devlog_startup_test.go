package tui

import (
	"strings"
	"testing"
)

// TestStartupDevLogWarningsSeededOnce proves DevLogWarnings reach the error log
// once, at warning severity, and never the content view.
func TestStartupDevLogWarningsSeededOnce(t *testing.T) {
	t.Parallel()
	const warning = "paths.exclude_patterns entry 2: unmatched brace; the entry is ignored"
	sink := &fakeDevLogSink{enabled: true}
	m := newModel(Config{Diagnostics: sink, DevLogWarnings: []string{warning}}, nil)

	if len(m.devLog.entries) != 1 {
		t.Fatalf("dev log entries = %d, want 1", len(m.devLog.entries))
	}
	entry := m.devLog.entries[0]
	if entry.Message != warning {
		t.Errorf("dev log message = %q, want %q", entry.Message, warning)
	}
	if entry.Severity != devLogWarning {
		t.Errorf("dev log severity = %v, want warning", entry.Severity)
	}
	if entry.Source != "config" {
		t.Errorf("dev log source = %q, want config", entry.Source)
	}
	if m.devLog.unseen != 1 || m.status.devLogUnseen != 1 {
		t.Errorf("unseen = %d (status %d), want 1", m.devLog.unseen, m.status.devLogUnseen)
	}
	if len(sink.records) != 1 {
		t.Fatalf("diagnostics records = %d, want 1: seeded after the sink is attached", len(sink.records))
	}
	for _, segment := range m.content.segments {
		if strings.Contains(segment.text, warning) {
			t.Fatalf("startup dev log warning reached the content view: %q", segment.text)
		}
	}
}

// TestStartupDevLogWarningsAbsentWhenNoneConfigured proves a startup without
// dev-log-only warnings records nothing.
func TestStartupDevLogWarningsAbsentWhenNoneConfigured(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	if len(m.devLog.entries) != 0 {
		t.Fatalf("dev log entries = %d, want 0", len(m.devLog.entries))
	}
	if m.devLog.unseen != 0 {
		t.Fatalf("unseen = %d, want 0", m.devLog.unseen)
	}
}
