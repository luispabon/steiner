package output

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestDelegationStartedEvent(t *testing.T) {
	tests := []struct {
		name        string
		agentID     string
		taskPreview string
		wantTrunc   bool
	}{
		{
			name:        "short preview",
			agentID:     "child-1",
			taskPreview: "fix bug",
			wantTrunc:   false,
		},
		{
			name:        "long preview truncated",
			agentID:     "child-2",
			taskPreview: "This is a very long task description that exceeds one hundred and twenty characters total length and should be truncated with ellipsis at the end",
			wantTrunc:   true,
		},
		{
			name:        "exactly 120 chars",
			agentID:     "child-3",
			taskPreview: "x" + string(make([]byte, 119)), // 120 chars total
			wantTrunc:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := NewDelegationStartedEvent(tt.agentID, tt.taskPreview)

			if event.Type != EventTypeDelegationStarted {
				t.Errorf("Type = %s, want %s", event.Type, EventTypeDelegationStarted)
			}

			payload, ok := event.Payload.(DelegationStartedEvent)
			if !ok {
				t.Fatalf("Payload type assertion failed")
			}

			if payload.AgentID != tt.agentID {
				t.Errorf("AgentID = %s, want %s", payload.AgentID, tt.agentID)
			}

			if tt.wantTrunc {
				if len(payload.TaskPreview) > 120 {
					t.Errorf("TaskPreview length %d exceeds max 120", len(payload.TaskPreview))
				}
				if !containsEllipsis(payload.TaskPreview) {
					t.Errorf("TaskPreview should end with '...', got %q", payload.TaskPreview)
				}
				// Total length including ellipsis should not exceed max
				if len(payload.TaskPreview) > 120 {
					t.Errorf("TaskPreview total length %d exceeds 120", len(payload.TaskPreview))
				}
			}
			if !tt.wantTrunc && payload.TaskPreview != tt.taskPreview {
				t.Errorf("TaskPreview = %q, want %q", payload.TaskPreview, tt.taskPreview)
			}

			// Test JSON round-trip
			data, err := json.Marshal(event)
			if err != nil {
				t.Fatalf("json.Marshal failed: %v", err)
			}

			var roundTrip Event
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatalf("json.Unmarshal failed: %v", err)
			}

			if roundTrip.Type != event.Type {
				t.Errorf("round-trip Type mismatch")
			}
		})
	}
}

func TestDelegationStartedEventWithModel(t *testing.T) {
	event := NewDelegationStartedEventWithType("child-1", "inspect", "call-1", "  deepseek-v4-flash  ", "")
	if event.Type != EventTypeDelegationStarted {
		t.Fatalf("Type = %s, want %s", event.Type, EventTypeDelegationStarted)
	}
	payload, ok := event.Payload.(DelegationStartedEvent)
	if !ok {
		t.Fatalf("Payload type assertion failed")
	}
	if payload.AgentID != "child-1" {
		t.Errorf("AgentID = %q, want %q", payload.AgentID, "child-1")
	}
	if payload.CallID != "call-1" {
		t.Errorf("CallID = %q, want %q", payload.CallID, "call-1")
	}
	if payload.ModelAlias != "deepseek-v4-flash" {
		t.Errorf("ModelAlias = %q, want %q", payload.ModelAlias, "deepseek-v4-flash")
	}
}

func TestDelegationCompleteEvent(t *testing.T) {
	tests := []struct {
		name    string
		agentID string
		status  string
		turns   int
		tokens  int
	}{
		{"success", "child-1", "complete", 3, 1500},
		{"failed", "child-2", "failed", 0, 0},
		{"pending", "child-3", "pending", 1, 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := NewDelegationCompleteEvent(DelegationCompleteParams{
				AgentID:       tt.agentID,
				Status:        tt.status,
				TurnCount:     tt.turns,
				TokenCount:    tt.tokens,
				ToolCallCount: 0,
				Output:        "",
			})

			if event.Type != EventTypeDelegationComplete {
				t.Errorf("Type = %s, want %s", event.Type, EventTypeDelegationComplete)
			}

			payload, ok := event.Payload.(DelegationCompleteEvent)
			if !ok {
				t.Fatalf("Payload type assertion failed")
			}

			if payload.AgentID != tt.agentID {
				t.Errorf("AgentID = %s, want %s", payload.AgentID, tt.agentID)
			}
			if payload.Status != tt.status {
				t.Errorf("Status = %s, want %s", payload.Status, tt.status)
			}
			if payload.TurnCount != tt.turns {
				t.Errorf("TurnCount = %d, want %d", payload.TurnCount, tt.turns)
			}
			if payload.TokenCount != tt.tokens {
				t.Errorf("TokenCount = %d, want %d", payload.TokenCount, tt.tokens)
			}

			// Test JSON round-trip
			data, err := json.Marshal(event)
			if err != nil {
				t.Fatalf("json.Marshal failed: %v", err)
			}

			var roundTrip Event
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatalf("json.Unmarshal failed: %v", err)
			}

			if roundTrip.Type != event.Type {
				t.Errorf("round-trip Type mismatch")
			}
		})
	}
}

func TestDelegationFailedEvent(t *testing.T) {
	tests := []struct {
		name        string
		agentID     string
		taskPreview string
		error       string
		wantTrunc   bool
	}{
		{
			name:        "short task, short error",
			agentID:     "child-1",
			taskPreview: "small task",
			error:       "timeout",
			wantTrunc:   false,
		},
		{
			name:        "long task truncated",
			agentID:     "child-2",
			taskPreview: string(make([]byte, 150)),
			error:       "context exceeded",
			wantTrunc:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := NewDelegationFailedEvent(DelegationFailedParams{
				AgentID:     tt.agentID,
				TaskPreview: tt.taskPreview,
				Error:       tt.error,
			})

			if event.Type != EventTypeDelegationFailed {
				t.Errorf("Type = %s, want %s", event.Type, EventTypeDelegationFailed)
			}

			payload, ok := event.Payload.(DelegationFailedEvent)
			if !ok {
				t.Fatalf("Payload type assertion failed")
			}

			if payload.AgentID != tt.agentID {
				t.Errorf("AgentID = %s, want %s", payload.AgentID, tt.agentID)
			}
			if payload.Error != tt.error {
				t.Errorf("Error = %s, want %s", payload.Error, tt.error)
			}

			if tt.wantTrunc {
				if len(payload.TaskPreview) > 120 {
					t.Errorf("TaskPreview length %d exceeds max 120", len(payload.TaskPreview))
				}
				if !containsEllipsis(payload.TaskPreview) {
					t.Errorf("TaskPreview should end with '...', got %q", payload.TaskPreview)
				}
			}

			// Test JSON round-trip
			data, err := json.Marshal(event)
			if err != nil {
				t.Fatalf("json.Marshal failed: %v", err)
			}

			var roundTrip Event
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatalf("json.Unmarshal failed: %v", err)
			}

			if roundTrip.Type != event.Type {
				t.Errorf("round-trip Type mismatch")
			}
		})
	}
}

func containsEllipsis(s string) bool {
	return len(s) >= 3 && s[len(s)-3:] == "..."
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"trace", slog.Level(-8)},
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"unknown", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"  info  ", slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseLevel(tt.input)
			if got != tt.want {
				t.Errorf("parseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSlogPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty input", input: "", want: ""},
		{name: "whitespace only", input: "   ", want: ""},
		{name: "with .log extension", input: "foo.log", want: "foo.slog"},
		{name: "absolute path with .log extension", input: "/tmp/session.log", want: "/tmp/session.slog"},
		{name: "no extension", input: "myfile", want: "myfile.slog"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SlogPath(tt.input)
			if got != tt.want {
				t.Errorf("SlogPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestConfigureLogger(t *testing.T) {
	tests := []struct {
		name      string
		level     string
		wantDebug bool
		wantInfo  bool
	}{
		{name: "debug level emits debug", level: "debug", wantDebug: true},
		{name: "info level drops debug", level: "info", wantInfo: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			ConfigureLogger(&buf, tt.level)

			slog.Debug("debug message")
			slog.Info("info message")

			got := buf.String()
			if tt.wantDebug && !strings.Contains(got, "debug message") {
				t.Fatalf("expected debug message in output, got %q", got)
			}
			if tt.wantInfo {
				if strings.Contains(got, "debug message") {
					t.Fatalf("expected debug message to be dropped at info level, got %q", got)
				}
				if !strings.Contains(got, "info message") {
					t.Fatalf("expected info message in output, got %q", got)
				}
			}
		})
	}
}

func TestConfigureLoggerDiscard(t *testing.T) {
	var first bytes.Buffer
	ConfigureLogger(&first, "debug")
	slog.Debug("goes to first")
	if !strings.Contains(first.String(), "goes to first") {
		t.Fatalf("expected message in first buffer, got %q", first.String())
	}

	var second bytes.Buffer
	ConfigureLogger(&second, "debug")
	slog.Log(context.Background(), slog.LevelDebug, "goes to second")
	if strings.Contains(first.String(), "goes to second") {
		t.Fatalf("logger was not reconfigured away from first")
	}
	if !strings.Contains(second.String(), "goes to second") {
		t.Fatalf("expected message in second buffer, got %q", second.String())
	}
}
