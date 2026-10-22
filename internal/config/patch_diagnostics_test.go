package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultConfigDiagnostics(t *testing.T) {
	cfg := defaultConfig(nil)
	if cfg.Diagnostics.Enabled {
		t.Error("Diagnostics.Enabled = true, want false")
	}
	if cfg.Diagnostics.CaptureBodies {
		t.Error("Diagnostics.CaptureBodies = true, want false")
	}
	if cfg.Diagnostics.RetentionDays != 30 {
		t.Errorf("Diagnostics.RetentionDays = %d, want 30", cfg.Diagnostics.RetentionDays)
	}
	want := filepath.Join("~", ".local", "state", "steiner", "diagnostics")
	if cfg.Diagnostics.Dir != want {
		t.Errorf("Diagnostics.Dir = %q, want %q", cfg.Diagnostics.Dir, want)
	}
	if cfg.Diagnostics.Streams != (DiagnosticsStreamsConfig{}) {
		t.Errorf("Diagnostics.Streams = %+v, want every stream off", cfg.Diagnostics.Streams)
	}
}

func TestDefaultDiagnosticsDirHonoursXDGStateHome(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "with XDG_STATE_HOME set",
			env:  map[string]string{"XDG_STATE_HOME": "/state"},
			want: filepath.Join("/state", "steiner", "diagnostics"),
		},
		{
			name: "with empty environment",
			env:  map[string]string{},
			want: filepath.Join("~", ".local", "state", "steiner", "diagnostics"),
		},
		{
			name: "with nil environment",
			env:  nil,
			want: filepath.Join("~", ".local", "state", "steiner", "diagnostics"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultDiagnosticsDir(tt.env); got != tt.want {
				t.Errorf("defaultDiagnosticsDir(%v) = %q, want %q", tt.env, got, tt.want)
			}
		})
	}
}

func TestApplyDiagnosticsPatch(t *testing.T) {
	enabled := true
	dir := "/var/state/diag"
	retention := 7
	captureBodies := true
	cacheOn := true
	toolOff := false

	tests := []struct {
		name  string
		start DiagnosticsConfig
		patch *diagnosticsPatch
		want  DiagnosticsConfig
	}{
		{
			name:  "empty patch keeps defaults",
			start: DiagnosticsConfig{Dir: "/base", RetentionDays: 30},
			patch: &diagnosticsPatch{},
			want:  DiagnosticsConfig{Dir: "/base", RetentionDays: 30},
		},
		{
			name:  "scalars override",
			start: DiagnosticsConfig{Dir: "/base", RetentionDays: 30},
			patch: &diagnosticsPatch{Enabled: &enabled, Dir: &dir, RetentionDays: &retention, CaptureBodies: &captureBodies},
			want:  DiagnosticsConfig{Enabled: true, Dir: dir, RetentionDays: 7, CaptureBodies: true},
		},
		{
			name:  "streams merge field by field",
			start: DiagnosticsConfig{Streams: DiagnosticsStreamsConfig{Provider: true, Tool: true}},
			patch: &diagnosticsPatch{Streams: &diagnosticsStreamsPatch{Cache: &cacheOn, Tool: &toolOff}},
			want:  DiagnosticsConfig{Streams: DiagnosticsStreamsConfig{Cache: true, Provider: true}},
		},
		{
			name:  "tui stream merges independently",
			start: DiagnosticsConfig{Streams: DiagnosticsStreamsConfig{Cache: true}},
			patch: &diagnosticsPatch{Streams: &diagnosticsStreamsPatch{TUI: &cacheOn}},
			want:  DiagnosticsConfig{Streams: DiagnosticsStreamsConfig{Cache: true, TUI: true}},
		},
		{
			name:  "internal stream merges independently",
			start: DiagnosticsConfig{Streams: DiagnosticsStreamsConfig{Cache: true}},
			patch: &diagnosticsPatch{Streams: &diagnosticsStreamsPatch{Internal: &cacheOn}},
			want:  DiagnosticsConfig{Streams: DiagnosticsStreamsConfig{Cache: true, Internal: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.start
			applyDiagnosticsPatch(&got, tt.patch)
			if got != tt.want {
				t.Errorf("applyDiagnosticsPatch() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDiagnosticsInternalStreamYAML(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want bool
	}{
		{name: "unset defaults to false", yaml: "diagnostics:\n  enabled: true\n", want: false},
		{name: "true parses", yaml: "diagnostics:\n  streams:\n    internal: true\n", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch, err := parseConfigPatch(tt.yaml)
			if err != nil {
				t.Fatalf("parseConfigPatch() error = %v", err)
			}
			cfg := defaultConfig(nil)
			applyPatch(&cfg, patch)
			if cfg.Diagnostics.Streams.Internal != tt.want {
				t.Errorf("Diagnostics.Streams.Internal = %v, want %v", cfg.Diagnostics.Streams.Internal, tt.want)
			}
		})
	}
}
