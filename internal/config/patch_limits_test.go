package config

import (
	"strings"
	"testing"
)

// TestLoadValidatesBashTimeoutWholeSeconds exercises the loaded and merged
// configuration path: a valid bash cap overrides the built-in default while
// other per-tool entries survive, and invalid caps fail validation after the
// merge.
func TestLoadValidatesBashTimeoutWholeSeconds(t *testing.T) {
	tests := []struct {
		name     string
		bash     string
		omitBash bool
		read     string
		want     Duration
		wantRead Duration
		wantErr  string
	}{
		{
			name: "whole seconds accepted and merged",
			bash: "300s",
			want: MustDuration("300s"),
		},
		{
			name:    "fractional milliseconds rejected",
			bash:    "500ms",
			wantErr: `limits.tool_timeouts["bash"] must be a whole number of seconds`,
		},
		{
			name:    "fractional seconds rejected",
			bash:    "1.5s",
			wantErr: `limits.tool_timeouts["bash"] must be a whole number of seconds`,
		},
		{
			name:    "zero rejected",
			bash:    "0s",
			wantErr: `limits.tool_timeouts["bash"] must be greater than zero`,
		},
		{
			name:    "negative rejected",
			bash:    "-10s",
			wantErr: `limits.tool_timeouts["bash"] must be greater than zero`,
		},
		{
			name:     "bash omitted uses 120s default",
			omitBash: true,
			want:     MustDuration("120s"),
		},
		{
			name:     "sub-second non-bash timeout retained",
			bash:     "300s",
			read:     "500ms",
			want:     MustDuration("300s"),
			wantRead: MustDuration("500ms"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var entries strings.Builder
			entries.WriteString("limits:\n  tool_timeouts:\n")
			if !tt.omitBash {
				entries.WriteString("    bash: " + tt.bash + "\n")
			}
			if tt.read != "" {
				entries.WriteString("    read: " + tt.read + "\n")
			}
			contents := entries.String()
			cfg, err := loadProfileTestConfigResult(t, contents, CLIOverrides{}, map[string]string{})
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Load() error = nil, want substring %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Load() error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got := cfg.Limits.ToolTimeouts["bash"]; got != tt.want {
				t.Errorf("Limits.ToolTimeouts[bash] = %v, want %v", got, tt.want)
			}
			wantRead := tt.wantRead
			if wantRead == (Duration{}) {
				wantRead = MustDuration("5s")
			}
			if got := cfg.Limits.ToolTimeouts["read"]; got != wantRead {
				t.Errorf("Limits.ToolTimeouts[read] = %v, want %v", got, wantRead)
			}
		})
	}
}
