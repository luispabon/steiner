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
		base      DelegationLimits
		overrides DelegationLimits
		want      DelegationLimits
	}{
		{
			name:      "zero overrides apply no changes",
			base:      DelegationLimits{MaxTurns: 20, OutputLimitTokens: 100000, Timeout: time.Minute},
			overrides: DelegationLimits{},
			want:      DelegationLimits{MaxTurns: 20, OutputLimitTokens: 100000, Timeout: time.Minute},
		},
		{
			name:      "tighter MaxTurns is applied",
			base:      DelegationLimits{MaxTurns: 20},
			overrides: DelegationLimits{MaxTurns: 10},
			want:      DelegationLimits{MaxTurns: 10},
		},
		{
			name:      "looser MaxTurns is not applied",
			base:      DelegationLimits{MaxTurns: 10},
			overrides: DelegationLimits{MaxTurns: 20},
			want:      DelegationLimits{MaxTurns: 10},
		},
		{
			name:      "tighter OutputLimitTokens is applied",
			base:      DelegationLimits{OutputLimitTokens: 100000},
			overrides: DelegationLimits{OutputLimitTokens: 50000},
			want:      DelegationLimits{OutputLimitTokens: 50000},
		},
		{
			name:      "looser OutputLimitTokens is not applied",
			base:      DelegationLimits{OutputLimitTokens: 50000},
			overrides: DelegationLimits{OutputLimitTokens: 100000},
			want:      DelegationLimits{OutputLimitTokens: 50000},
		},
		{
			name:      "tighter Timeout is applied",
			base:      DelegationLimits{Timeout: time.Minute},
			overrides: DelegationLimits{Timeout: 30 * time.Second},
			want:      DelegationLimits{Timeout: 30 * time.Second},
		},
		{
			name:      "looser Timeout is not applied",
			base:      DelegationLimits{Timeout: 30 * time.Second},
			overrides: DelegationLimits{Timeout: time.Minute},
			want:      DelegationLimits{Timeout: 30 * time.Second},
		},
		{
			name:      "zero base timeout can be overridden",
			base:      DelegationLimits{Timeout: 0},
			overrides: DelegationLimits{Timeout: time.Minute},
			want:      DelegationLimits{Timeout: time.Minute},
		},
		{
			name:      "multiple tightenings applied together",
			base:      DelegationLimits{MaxTurns: 30, OutputLimitTokens: 200000, Timeout: 2 * time.Minute},
			overrides: DelegationLimits{MaxTurns: 10, OutputLimitTokens: 50000, Timeout: 30 * time.Second},
			want:      DelegationLimits{MaxTurns: 10, OutputLimitTokens: 50000, Timeout: 30 * time.Second},
		},
		{
			name:      "partial overrides with selective tightening",
			base:      DelegationLimits{MaxTurns: 20, OutputLimitTokens: 100000},
			overrides: DelegationLimits{MaxTurns: 5}, // Only override MaxTurns, tighter
			want:      DelegationLimits{MaxTurns: 5, OutputLimitTokens: 100000},
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
