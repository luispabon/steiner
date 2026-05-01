package delegation

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

func TestDeriveChildLimits(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.SubAgentConfig
		overrides   DelegationLimits
		wantTurns   int
		wantTokens  int
		wantTimeout time.Duration
	}{
		{
			name:        "empty config and no overrides uses defaults",
			cfg:         config.SubAgentConfig{},
			overrides:   DelegationLimits{},
			wantTurns:   15,
			wantTokens:  100000,
			wantTimeout: 0,
		},
		{
			name:        "config defaults used when no overrides",
			cfg:         config.SubAgentConfig{MaxTurns: 20, MaxTokens: 200000},
			overrides:   DelegationLimits{},
			wantTurns:   20,
			wantTokens:  200000,
			wantTimeout: 0,
		},
		{
			name:        "override tightens max turns",
			cfg:         config.SubAgentConfig{MaxTurns: 15},
			overrides:   DelegationLimits{MaxTurns: 5},
			wantTurns:   5,
			wantTokens:  100000,
			wantTimeout: 0,
		},
		{
			name:        "looser override is ignored",
			cfg:         config.SubAgentConfig{MaxTurns: 15},
			overrides:   DelegationLimits{MaxTurns: 30},
			wantTurns:   15,
			wantTokens:  100000,
			wantTimeout: 0,
		},
		{
			name:        "timeout override applied",
			cfg:         config.SubAgentConfig{},
			overrides:   DelegationLimits{Timeout: 30 * time.Second},
			wantTurns:   15,
			wantTokens:  100000,
			wantTimeout: 30 * time.Second,
		},
		{
			name:        "tokens override tightens",
			cfg:         config.SubAgentConfig{MaxTokens: 100000},
			overrides:   DelegationLimits{OutputLimitTokens: 50000},
			wantTurns:   15,
			wantTokens:  50000,
			wantTimeout: 0,
		},
		{
			name:        "all fields override together",
			cfg:         config.SubAgentConfig{MaxTurns: 20, MaxTokens: 200000},
			overrides:   DelegationLimits{MaxTurns: 10, OutputLimitTokens: 50000, Timeout: time.Minute},
			wantTurns:   10,
			wantTokens:  50000,
			wantTimeout: time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveChildLimits(tt.cfg, tt.overrides)
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
