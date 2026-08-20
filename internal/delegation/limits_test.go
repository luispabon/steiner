package delegation

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

func TestDefaultLimits(t *testing.T) {
	tests := []struct {
		name                  string
		cfg                   config.SubAgentConfig
		wantTurns, wantTokens int
		wantTimeout           time.Duration
	}{
		{
			name:        "zero config uses defaults",
			cfg:         config.SubAgentConfig{},
			wantTurns:   15,
			wantTokens:  100000,
			wantTimeout: 0,
		},
		{
			name: "config values are used",
			cfg: config.SubAgentConfig{
				MaxTurns:  20,
				MaxTokens: 200000,
			},
			wantTurns:   20,
			wantTokens:  200000,
			wantTimeout: 0,
		},
		{
			name: "partial config uses some defaults",
			cfg: config.SubAgentConfig{
				MaxTurns: 10,
			},
			wantTurns:   10,
			wantTokens:  100000,
			wantTimeout: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DefaultLimits(tt.cfg)
			if got.MaxTurns != tt.wantTurns {
				t.Errorf("MaxTurns=%d, want %d", got.MaxTurns, tt.wantTurns)
			}
			if got.OutputLimitTokens != tt.wantTokens {
				t.Errorf("OutputLimitTokens=%d, want %d", got.OutputLimitTokens, tt.wantTokens)
			}
			if got.Timeout != tt.wantTimeout {
				t.Errorf("Timeout=%v, want %v", got.Timeout, tt.wantTimeout)
			}
		})
	}
}

func TestApplyOverridesTightenOnly(t *testing.T) {
	tests := []struct {
		name      string
		base      Limits
		overrides Limits
		want      Limits
	}{
		{
			name:      "zero overrides apply no changes",
			base:      Limits{MaxTurns: 20, OutputLimitTokens: 100000, Timeout: time.Minute},
			overrides: Limits{},
			want:      Limits{MaxTurns: 20, OutputLimitTokens: 100000, Timeout: time.Minute},
		},
		{
			name:      "tighter MaxTurns is applied",
			base:      Limits{MaxTurns: 20},
			overrides: Limits{MaxTurns: 10},
			want:      Limits{MaxTurns: 10},
		},
		{
			name:      "looser MaxTurns is not applied",
			base:      Limits{MaxTurns: 10},
			overrides: Limits{MaxTurns: 20},
			want:      Limits{MaxTurns: 10},
		},
		{
			name:      "tighter OutputLimitTokens is applied",
			base:      Limits{OutputLimitTokens: 100000},
			overrides: Limits{OutputLimitTokens: 50000},
			want:      Limits{OutputLimitTokens: 50000},
		},
		{
			name:      "looser OutputLimitTokens is not applied",
			base:      Limits{OutputLimitTokens: 50000},
			overrides: Limits{OutputLimitTokens: 100000},
			want:      Limits{OutputLimitTokens: 50000},
		},
		{
			name:      "tighter Timeout is applied",
			base:      Limits{Timeout: time.Minute},
			overrides: Limits{Timeout: 30 * time.Second},
			want:      Limits{Timeout: 30 * time.Second},
		},
		{
			name:      "looser Timeout is not applied",
			base:      Limits{Timeout: 30 * time.Second},
			overrides: Limits{Timeout: time.Minute},
			want:      Limits{Timeout: 30 * time.Second},
		},
		{
			name:      "zero base timeout can be overridden",
			base:      Limits{Timeout: 0},
			overrides: Limits{Timeout: time.Minute},
			want:      Limits{Timeout: time.Minute},
		},
		{
			name:      "multiple tightenings applied together",
			base:      Limits{MaxTurns: 30, OutputLimitTokens: 200000, Timeout: 2 * time.Minute},
			overrides: Limits{MaxTurns: 10, OutputLimitTokens: 50000, Timeout: 30 * time.Second},
			want:      Limits{MaxTurns: 10, OutputLimitTokens: 50000, Timeout: 30 * time.Second},
		},
		{
			name:      "partial overrides with selective tightening",
			base:      Limits{MaxTurns: 20, OutputLimitTokens: 100000},
			overrides: Limits{MaxTurns: 5}, // Only override MaxTurns, tighter
			want:      Limits{MaxTurns: 5, OutputLimitTokens: 100000},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyOverrides(tt.base, tt.overrides)
			if got.MaxTurns != tt.want.MaxTurns {
				t.Errorf("MaxTurns=%d, want %d", got.MaxTurns, tt.want.MaxTurns)
			}
			if got.OutputLimitTokens != tt.want.OutputLimitTokens {
				t.Errorf("OutputLimitTokens=%d, want %d", got.OutputLimitTokens, tt.want.OutputLimitTokens)
			}
			if got.Timeout != tt.want.Timeout {
				t.Errorf("Timeout=%v, want %v", got.Timeout, tt.want.Timeout)
			}
		})
	}
}
