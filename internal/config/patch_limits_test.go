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
		name    string
		bash    string
		want    Duration
		wantErr string
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contents := "limits:\n  tool_timeouts:\n    bash: " + tt.bash + "\n"
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
			if got, want := cfg.Limits.ToolTimeouts["read"], MustDuration("5s"); got != want {
				t.Errorf("Limits.ToolTimeouts[read] = %v, want default %v (merge clobbered other entries)", got, want)
			}
		})
	}
}
