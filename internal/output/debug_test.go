package output

import (
	"strings"
	"testing"
)

func TestFormatContextDiagnosticsEvent(t *testing.T) {
	tests := []struct {
		name    string
		payload ContextDiagnosticsEvent
		check   func(t *testing.T, result string)
	}{
		{
			name: "budget kind with bytes",
			payload: ContextDiagnosticsEvent{
				Kind:        "budget",
				Scope:       "context",
				UsedBytes:   500,
				BudgetBytes: 1000,
				Turn:        1,
			},
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, "budget") {
					t.Error("missing budget keyword")
				}
				if !strings.Contains(result, "500") || !strings.Contains(result, "1000") {
					t.Error("missing byte counts")
				}
			},
		},
		{
			name: "budget kind with tokens",
			payload: ContextDiagnosticsEvent{
				Kind:                "budget",
				PromptTokens:        100,
				ContextWindow:       4096,
				ContextUsagePercent: 42.5,
				Status:              "ok",
			},
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, "prompt_tokens=100") {
					t.Error("missing prompt_tokens")
				}
				if !strings.Contains(result, "context_window=4096") {
					t.Error("missing context_window")
				}
				if !strings.Contains(result, "42%") {
					t.Error("missing usage percent")
				}
			},
		},
		{
			name: "compaction kind",
			payload: ContextDiagnosticsEvent{
				Kind:              "compaction",
				CompactedTurns:    5,
				CompactedMessages: 20,
				RetainedTurns:     2,
				RetainedMessages:  8,
			},
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, "compaction") {
					t.Error("missing compaction keyword")
				}
				if !strings.Contains(result, "5") {
					t.Error("missing compacted turns count")
				}
			},
		},
		{
			name: "file_annotation kind",
			payload: ContextDiagnosticsEvent{
				Kind:   "file_annotation",
				Action: "read",
				Path:   "/tmp/test.txt",
				Reason: "context build",
			},
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, "file annotation") {
					t.Error("missing file annotation keyword")
				}
				if !strings.Contains(result, "/tmp/test.txt") {
					t.Error("missing path")
				}
			},
		},
		{
			name: "session_health kind",
			payload: ContextDiagnosticsEvent{
				Kind:            "session_health",
				CompactionCount: 3,
				Severity:        "warn",
			},
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, "session health") {
					t.Error("missing session health keyword")
				}
				if !strings.Contains(result, "3") {
					t.Error("missing compaction count")
				}
			},
		},
		{
			name: "unknown kind",
			payload: ContextDiagnosticsEvent{
				Kind:  "unknown_diagnostic",
				Scope: "test",
			},
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, "context diagnostics") {
					t.Error("missing fallback context diagnostics")
				}
				if !strings.Contains(result, "unknown_diagnostic") {
					t.Error("missing unknown kind")
				}
			},
		},
		{
			name: "empty kind defaults to diagnostic",
			payload: ContextDiagnosticsEvent{
				Kind: "",
			},
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, "context diagnostics") {
					t.Error("missing fallback for empty kind")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatContextDiagnosticsEvent(tt.payload)
			tt.check(t, result)
		})
	}
}

func TestFormatAnyContextDiagnosticsEvent(t *testing.T) {
	tests := []struct {
		name    string
		payload any
		check   func(t *testing.T, result string)
	}{
		{
			name: "typed compaction event",
			payload: ContextCompactionEvent{
				CompactedTurns:    3,
				CompactedMessages: 12,
				RetainedTurns:     1,
				RetainedMessages:  4,
			},
			check: func(t *testing.T, result string) {
				if result == "" {
					t.Error("expected non-empty result")
				}
				if !strings.Contains(result, "compaction") {
					t.Error("missing compaction")
				}
			},
		},
		{
			name: "typed session health event",
			payload: ContextSessionHealthEvent{
				CompactionCount: 2,
				Severity:        "info",
			},
			check: func(t *testing.T, result string) {
				if result == "" {
					t.Error("expected non-empty result")
				}
			},
		},
		{
			name: "typed budget event",
			payload: ContextBudgetEvent{
				UsedBytes:   100,
				BudgetBytes: 500,
			},
			check: func(t *testing.T, result string) {
				if result == "" {
					t.Error("expected non-empty result")
				}
			},
		},
		{
			name: "typed file annotation event",
			payload: ContextFileAnnotationEvent{
				Action: "skip",
				Path:   "file.txt",
			},
			check: func(t *testing.T, result string) {
				if result == "" {
					t.Error("expected non-empty result")
				}
			},
		},
		{
			name:    "non-diagnostic payload",
			payload: "not a diagnostic",
			check: func(t *testing.T, result string) {
				if result != "" {
					t.Error("expected empty result for non-diagnostic")
				}
			},
		},
		{
			name:    "nil payload",
			payload: nil,
			check: func(t *testing.T, result string) {
				if result != "" {
					t.Error("expected empty result for nil")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatAnyContextDiagnosticsEvent(tt.payload)
			tt.check(t, result)
		})
	}
}

func TestFormatCompactionUsage(t *testing.T) {
	tests := []struct {
		name    string
		payload ContextDiagnosticsEvent
		want    int // expected slice length
	}{
		{
			name: "all zero",
			payload: ContextDiagnosticsEvent{
				BeforePromptTokens: 0,
				BeforeUsagePercent: 0,
				AfterPromptTokens:  0,
				AfterUsagePercent:  0,
			},
			want: 0,
		},
		{
			name: "before tokens set",
			payload: ContextDiagnosticsEvent{
				BeforePromptTokens: 100,
				BeforeUsagePercent: 50.0,
				AfterPromptTokens:  80,
				AfterUsagePercent:  40.0,
			},
			want: 2,
		},
		{
			name: "only after tokens",
			payload: ContextDiagnosticsEvent{
				AfterPromptTokens: 50,
				AfterUsagePercent: 25.0,
			},
			want: 2,
		},
		{
			name: "fractional percentages",
			payload: ContextDiagnosticsEvent{
				BeforePromptTokens: 1000,
				BeforeUsagePercent: 33.333,
				AfterPromptTokens:  900,
				AfterUsagePercent:  30.0,
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatCompactionUsage(tt.payload)
			if len(result) != tt.want {
				t.Fatalf("length = %d, want %d", len(result), tt.want)
			}
		})
	}
}

func TestHumanizeDiagnosticScope(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"prompt", "prompt"},
		{"context_messages", "context messages"},
		{"project_files", "project files"},
		{"", ""},
		{"  ", ""},
		{"message_queue", "message queue"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := humanizeDiagnosticScope(tt.input)
			if result != tt.want {
				t.Fatalf("result = %q, want %q", result, tt.want)
			}
		})
	}
}

func TestJoinDiagnosticNotes(t *testing.T) {
	tests := []struct {
		name  string
		notes []string
		want  string
	}{
		{
			name:  "empty slice",
			notes: []string{},
			want:  "",
		},
		{
			name:  "single note",
			notes: []string{"note1"},
			want:  "note1",
		},
		{
			name:  "multiple notes",
			notes: []string{"note1", "note2"},
			want:  "note1, note2",
		},
		{
			name:  "notes with equals signs",
			notes: []string{"key=value", "other=data"},
			want:  "key value, other data",
		},
		{
			name:  "empty and whitespace notes filtered",
			notes: []string{"", "  ", "valid"},
			want:  "valid",
		},
		{
			name:  "nil slice",
			notes: nil,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := joinDiagnosticNotes(tt.notes)
			if result != tt.want {
				t.Fatalf("result = %q, want %q", result, tt.want)
			}
		})
	}
}

func TestFormatPercent(t *testing.T) {
	tests := []struct {
		value float64
		want  string
	}{
		{0.0, "0%"},
		{50.5, "50%"},
		{99.9, "100%"},
		{33.333, "33%"},
		{100.0, "100%"},
		{1.5, "2%"},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := formatPercent(tt.value)
			if result != tt.want {
				t.Fatalf("formatPercent(%v) = %q, want %q", tt.value, result, tt.want)
			}
		})
	}
}

func TestCompactionSummaryPreview(t *testing.T) {
	tests := []struct {
		name    string
		payload ContextDiagnosticsEvent
		want    string
	}{
		{
			name: "title and preview",
			payload: ContextDiagnosticsEvent{
				SummaryTitle:   "Summary",
				SummaryPreview: "This is a preview",
			},
			want: "Summary: This is a preview",
		},
		{
			name: "title only",
			payload: ContextDiagnosticsEvent{
				SummaryTitle: "Just Title",
			},
			want: "Just Title",
		},
		{
			name: "preview only",
			payload: ContextDiagnosticsEvent{
				SummaryPreview: "Just preview",
			},
			want: "Just preview",
		},
		{
			name:    "empty",
			payload: ContextDiagnosticsEvent{},
			want:    "",
		},
		{
			name: "whitespace stripped",
			payload: ContextDiagnosticsEvent{
				SummaryTitle:   "  Title  ",
				SummaryPreview: "  Preview  ",
			},
			want: "Title: Preview",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compactionSummaryPreview(tt.payload)
			if result != tt.want {
				t.Fatalf("result = %q, want %q", result, tt.want)
			}
		})
	}
}

func TestFormatDiagnosticHeadline(t *testing.T) {
	tests := []struct {
		name    string
		payload ContextDiagnosticsEvent
		subject string
		check   func(t *testing.T, result string)
	}{
		{
			name: "with severity",
			payload: ContextDiagnosticsEvent{
				Severity: "warn",
			},
			subject: "compaction",
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, "warn:") {
					t.Error("missing severity")
				}
			},
		},
		{
			name: "with turn",
			payload: ContextDiagnosticsEvent{
				Turn: 5,
			},
			subject: "budget",
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, "turn 5") {
					t.Error("missing turn")
				}
			},
		},
		{
			name: "compaction subject includes counts",
			payload: ContextDiagnosticsEvent{
				CompactedTurns:    2,
				CompactedMessages: 8,
				RetainedTurns:     1,
				RetainedMessages:  3,
			},
			subject: "compaction",
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, "2") || !strings.Contains(result, "8") {
					t.Error("missing compacted counts")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDiagnosticHeadline(tt.payload, tt.subject)
			tt.check(t, result)
		})
	}
}

func TestFormatDiagnosticEscalation(t *testing.T) {
	tests := []struct {
		name    string
		payload ContextDiagnosticsEvent
		check   func(t *testing.T, result []string)
	}{
		{
			name: "with session state",
			payload: ContextDiagnosticsEvent{
				SessionState: "degraded",
			},
			check: func(t *testing.T, result []string) {
				if len(result) == 0 {
					t.Error("expected non-empty result")
				}
				found := false
				for _, s := range result {
					if strings.Contains(s, "degraded") {
						found = true
						break
					}
				}
				if !found {
					t.Error("missing session state")
				}
			},
		},
		{
			name: "with restart guidance",
			payload: ContextDiagnosticsEvent{
				RestartGuidance: "start fresh",
			},
			check: func(t *testing.T, result []string) {
				if len(result) == 0 {
					t.Error("expected non-empty result")
				}
				found := false
				for _, s := range result {
					if s == "start fresh" {
						found = true
						break
					}
				}
				if !found {
					t.Error("missing restart guidance")
				}
			},
		},
		{
			name: "budget kind with compactions",
			payload: ContextDiagnosticsEvent{
				Kind:            "budget",
				CompactionCount: 3,
			},
			check: func(t *testing.T, result []string) {
				// budget kind DOES include compactions in escalation (unlike session_health)
				found := false
				for _, s := range result {
					if strings.Contains(s, "compactions 3") {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected compactions in budget escalation")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDiagnosticEscalation(tt.payload)
			tt.check(t, result)
		})
	}
}

func TestFormatGenericContextDiagnostics(t *testing.T) {
	payload := ContextDiagnosticsEvent{
		Kind:  "custom_kind",
		Scope: "custom_scope",
		Turn:  7,
		Notes: []string{"note1", "note2"},
	}
	result := formatGenericContextDiagnostics(payload)
	if !strings.Contains(result, "custom_kind") {
		t.Error("missing kind")
	}
	if !strings.Contains(result, "custom_scope") {
		t.Error("missing scope")
	}
	if !strings.Contains(result, "7") {
		t.Error("missing turn")
	}
}
