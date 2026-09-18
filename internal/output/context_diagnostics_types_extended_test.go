package output

import (
	"testing"
)

func TestContextDiagnosticKind(t *testing.T) {
	tests := []struct {
		name    string
		payload any
		want    string
	}{
		{
			name: "compaction event",
			payload: ContextCompactionEvent{
				CompactedTurns: 1,
			},
			want: "compaction",
		},
		{
			name: "session health event",
			payload: ContextSessionHealthEvent{
				CompactionCount: 1,
			},
			want: "session_health",
		},
		{
			name: "budget event",
			payload: ContextBudgetEvent{
				UsedBytes: 100,
			},
			want: "budget",
		},
		{
			name: "file annotation event",
			payload: ContextFileAnnotationEvent{
				Path: "test.txt",
			},
			want: "file_annotation",
		},
		{
			name: "legacy diagnostics event",
			payload: ContextDiagnosticsEvent{
				Kind: "compaction",
			},
			want: "compaction",
		},
		{
			name:    "non-diagnostic payload",
			payload: "not diagnostic",
			want:    "",
		},
		{
			name:    "nil payload",
			payload: nil,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ContextDiagnosticKind(tt.payload)
			if result != tt.want {
				t.Fatalf("ContextDiagnosticKind() = %q, want %q", result, tt.want)
			}
		})
	}
}

func TestContextDiagnosticSeverity(t *testing.T) {
	tests := []struct {
		name    string
		payload any
		want    string
	}{
		{
			name: "with severity",
			payload: ContextSessionHealthEvent{
				Severity: "warn",
			},
			want: "warn",
		},
		{
			name: "budget event (no severity field)",
			payload: ContextBudgetEvent{
				UsedBytes: 100,
			},
			want: "",
		},
		{
			name:    "non-diagnostic",
			payload: "string",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ContextDiagnosticSeverity(tt.payload)
			if result != tt.want {
				t.Fatalf("result = %q, want %q", result, tt.want)
			}
		})
	}
}

func TestContextDiagnosticNotes(t *testing.T) {
	tests := []struct {
		name    string
		payload any
		want    []string
	}{
		{
			name: "with notes",
			payload: ContextSessionHealthEvent{
				Notes: []string{"note1", "note2"},
			},
			want: []string{"note1", "note2"},
		},
		{
			name: "empty notes",
			payload: ContextBudgetEvent{
				Notes: []string{},
			},
			want: nil,
		},
		{
			name:    "non-diagnostic",
			payload: 42,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ContextDiagnosticNotes(tt.payload)
			if len(result) != len(tt.want) {
				t.Fatalf("length = %d, want %d", len(result), len(tt.want))
			}
			for i, note := range result {
				if i < len(tt.want) && note != tt.want[i] {
					t.Fatalf("[%d] = %q, want %q", i, note, tt.want[i])
				}
			}
		})
	}
}

func TestContextDiagnosticReason(t *testing.T) {
	tests := []struct {
		name    string
		payload any
		want    string
	}{
		{
			name: "with reason",
			payload: ContextFileAnnotationEvent{
				Reason: "context build",
			},
			want: "context build",
		},
		{
			name: "empty reason",
			payload: ContextFileAnnotationEvent{
				Reason: "",
			},
			want: "",
		},
		{
			name:    "non-diagnostic",
			payload: true,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ContextDiagnosticReason(tt.payload)
			if result != tt.want {
				t.Fatalf("result = %q, want %q", result, tt.want)
			}
		})
	}
}

func TestAsContextCompactionEvent(t *testing.T) {
	tests := []struct {
		name    string
		payload any
		wantOk  bool
		check   func(t *testing.T, event ContextCompactionEvent)
	}{
		{
			name: "typed compaction event",
			payload: ContextCompactionEvent{
				CompactedTurns:    5,
				CompactedMessages: 20,
			},
			wantOk: true,
			check: func(t *testing.T, event ContextCompactionEvent) {
				if event.CompactedTurns != 5 {
					t.Errorf("CompactedTurns = %d", event.CompactedTurns)
				}
			},
		},
		{
			name: "legacy compaction event",
			payload: ContextDiagnosticsEvent{
				Kind:              "compaction",
				CompactedTurns:    3,
				CompactedMessages: 10,
			},
			wantOk: true,
			check: func(t *testing.T, event ContextCompactionEvent) {
				if event.CompactedTurns != 3 {
					t.Errorf("CompactedTurns = %d", event.CompactedTurns)
				}
			},
		},
		{
			name: "wrong kind",
			payload: ContextDiagnosticsEvent{
				Kind: "budget",
			},
			wantOk: false,
		},
		{
			name:    "non-diagnostic",
			payload: "string",
			wantOk:  false,
		},
		{
			name:    "nil",
			payload: nil,
			wantOk:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := AsContextCompactionEvent(tt.payload)
			if ok != tt.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOk)
			}
			if ok && tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestAsContextSessionHealthEvent(t *testing.T) {
	tests := []struct {
		name    string
		payload any
		wantOk  bool
	}{
		{
			name: "typed session health",
			payload: ContextSessionHealthEvent{
				CompactionCount: 2,
			},
			wantOk: true,
		},
		{
			name: "legacy session health",
			payload: ContextDiagnosticsEvent{
				Kind:            "session_health",
				CompactionCount: 1,
			},
			wantOk: true,
		},
		{
			name: "wrong kind",
			payload: ContextDiagnosticsEvent{
				Kind: "budget",
			},
			wantOk: false,
		},
		{
			name:    "non-diagnostic",
			payload: 123,
			wantOk:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := AsContextSessionHealthEvent(tt.payload)
			if ok != tt.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOk)
			}
		})
	}
}

func TestAsContextBudgetEvent(t *testing.T) {
	tests := []struct {
		name    string
		payload any
		wantOk  bool
	}{
		{
			name: "typed budget",
			payload: ContextBudgetEvent{
				UsedBytes: 100,
			},
			wantOk: true,
		},
		{
			name: "legacy budget",
			payload: ContextDiagnosticsEvent{
				Kind:      "budget",
				UsedBytes: 50,
			},
			wantOk: true,
		},
		{
			name: "session_loaded kind (also budget)",
			payload: ContextDiagnosticsEvent{
				Kind: "session_loaded",
			},
			wantOk: true,
		},
		{
			name: "wrong kind",
			payload: ContextDiagnosticsEvent{
				Kind: "compaction",
			},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := AsContextBudgetEvent(tt.payload)
			if ok != tt.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOk)
			}
		})
	}
}

func TestAsContextFileAnnotationEvent(t *testing.T) {
	tests := []struct {
		name    string
		payload any
		wantOk  bool
		check   func(t *testing.T, event ContextFileAnnotationEvent)
	}{
		{
			name: "typed file annotation",
			payload: ContextFileAnnotationEvent{
				Path:   "file.txt",
				Action: "skip",
			},
			wantOk: true,
			check: func(t *testing.T, event ContextFileAnnotationEvent) {
				if event.Path != "file.txt" {
					t.Errorf("Path = %q", event.Path)
				}
			},
		},
		{
			name: "legacy file annotation",
			payload: ContextDiagnosticsEvent{
				Kind:   "file_annotation",
				Path:   "other.txt",
				Action: "read",
			},
			wantOk: true,
			check: func(t *testing.T, event ContextFileAnnotationEvent) {
				if event.Action != "read" {
					t.Errorf("Action = %q", event.Action)
				}
			},
		},
		{
			name: "wrong kind",
			payload: ContextDiagnosticsEvent{
				Kind: "budget",
			},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := AsContextFileAnnotationEvent(tt.payload)
			if ok != tt.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOk)
			}
			if ok && tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestCloneContextDiagnosticPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload any
		wantOk  bool
		check   func(t *testing.T, src, cloned any)
	}{
		{
			name: "clone compaction event",
			payload: ContextCompactionEvent{
				CompactedTurns: 5,
				Notes:          []string{"note1"},
			},
			wantOk: true,
			check: func(t *testing.T, src, cloned any) {
				ce, ok := cloned.(ContextCompactionEvent)
				if !ok {
					t.Fatalf("type = %T", cloned)
				}
				if ce.CompactedTurns != 5 {
					t.Errorf("CompactedTurns = %d", ce.CompactedTurns)
				}
				if ce.Notes[0] != "note1" {
					t.Errorf("Notes[0] = %q, want %q", ce.Notes[0], "note1")
				}
				ce.Notes[0] = "modified"
				srcCE := src.(ContextCompactionEvent)
				if srcCE.Notes[0] != "note1" {
					t.Errorf("source Notes[0] was mutated to %q, clone should be independent", srcCE.Notes[0])
				}
			},
		},
		{
			name: "clone budget event with notes",
			payload: ContextBudgetEvent{
				UsedBytes: 100,
				Notes:     []string{"a", "b", "c"},
			},
			wantOk: true,
			check: func(t *testing.T, src, cloned any) {
				be, ok := cloned.(ContextBudgetEvent)
				if !ok {
					t.Fatalf("type = %T", cloned)
				}
				if be.UsedBytes != 100 {
					t.Errorf("UsedBytes = %d", be.UsedBytes)
				}
				if len(be.Notes) != 3 {
					t.Errorf("Notes length = %d", len(be.Notes))
				}
				if be.Notes[0] != "a" || be.Notes[1] != "b" || be.Notes[2] != "c" {
					t.Errorf("Notes = %v, want [a b c]", be.Notes)
				}
				be.Notes[0] = "modified"
				srcBE := src.(ContextBudgetEvent)
				if srcBE.Notes[0] != "a" {
					t.Errorf("source Notes[0] was mutated to %q, clone should be independent", srcBE.Notes[0])
				}
			},
		},
		{
			name: "clone file annotation event",
			payload: ContextFileAnnotationEvent{
				Path:  "test.txt",
				Notes: []string{"note"},
			},
			wantOk: true,
			check: func(t *testing.T, src, cloned any) {
				fae, ok := cloned.(ContextFileAnnotationEvent)
				if !ok {
					t.Fatalf("type = %T", cloned)
				}
				if fae.Path != "test.txt" {
					t.Errorf("Path = %q", fae.Path)
				}
				if fae.Notes[0] != "note" {
					t.Errorf("Notes[0] = %q, want %q", fae.Notes[0], "note")
				}
				fae.Notes[0] = "modified"
				srcFAE := src.(ContextFileAnnotationEvent)
				if srcFAE.Notes[0] != "note" {
					t.Errorf("source Notes[0] was mutated to %q, clone should be independent", srcFAE.Notes[0])
				}
			},
		},
		{
			name:    "non-diagnostic payload",
			payload: "string",
			wantOk:  false,
		},
		{
			name:    "nil payload",
			payload: nil,
			wantOk:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cloned, ok := CloneContextDiagnosticPayload(tt.payload)
			if ok != tt.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOk)
			}
			if ok && tt.check != nil {
				tt.check(t, tt.payload, cloned)
			}
		})
	}
}
