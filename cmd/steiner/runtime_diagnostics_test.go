package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/diagnostics"
)

func TestBuildRuntimeDiagnosticsDisabledCreatesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "diagnostics")
	cfg := config.Config{Diagnostics: config.DiagnosticsConfig{
		Enabled:       false,
		Dir:           dir,
		RetentionDays: 30,
		Streams:       config.DiagnosticsStreamsConfig{Cache: true, Provider: true, Tool: true},
	}}
	writer, err := buildRuntimeDiagnostics(cfg)
	if err != nil {
		t.Fatalf("buildRuntimeDiagnostics() error = %v", err)
	}
	if writer != nil {
		t.Fatalf("writer = %#v, want nil when diagnostics are disabled", writer)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("stat %s error = %v, want no directory when diagnostics are disabled", dir, err)
	}
}

func TestBuildRuntimeDiagnosticsStreamGates(t *testing.T) {
	tests := []struct {
		name    string
		streams config.DiagnosticsStreamsConfig
		want    map[diagnostics.Kind]bool
	}{
		{
			name:    "no stream selected builds no writer",
			streams: config.DiagnosticsStreamsConfig{},
			want:    map[diagnostics.Kind]bool{},
		},
		{
			name:    "cache and provider only",
			streams: config.DiagnosticsStreamsConfig{Cache: true, Provider: true},
			want:    map[diagnostics.Kind]bool{diagnostics.KindCache: true, diagnostics.KindProvider: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "diagnostics")
			cfg := config.Config{Diagnostics: config.DiagnosticsConfig{
				Enabled:       true,
				Dir:           dir,
				RetentionDays: 30,
				Streams:       tt.streams,
				CaptureBodies: true,
			}}
			writer, err := buildRuntimeDiagnostics(cfg)
			if err != nil {
				t.Fatalf("buildRuntimeDiagnostics() error = %v", err)
			}
			t.Cleanup(func() { _ = writer.Close() })
			for _, kind := range diagnostics.Kinds() {
				if writer.Enabled(kind) != tt.want[kind] {
					t.Errorf("Enabled(%s) = %v, want %v", kind, writer.Enabled(kind), tt.want[kind])
				}
				if got := streamWriter(writer, kind); (got != nil) != tt.want[kind] {
					t.Errorf("streamWriter(%s) non-nil = %v, want %v", kind, got != nil, tt.want[kind])
				}
			}
			if len(tt.want) == 0 {
				if writer != nil {
					t.Errorf("writer = %#v, want nil when every stream is off", writer)
				}
				return
			}
			if !writer.CaptureBodies() {
				t.Error("CaptureBodies() = false, want the configured value")
			}
			if writer.RunID() == "" {
				t.Error("RunID() = empty, want a minted run id")
			}
		})
	}
}

func TestBuildDirty(t *testing.T) {
	original := dirty
	t.Cleanup(func() { dirty = original })
	tests := []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "true", want: true},
		{value: "false", want: false},
	}
	for _, tt := range tests {
		dirty = tt.value
		if got := buildDirty(); got != tt.want {
			t.Errorf("buildDirty() with dirty=%q = %v, want %v", tt.value, got, tt.want)
		}
	}
}
