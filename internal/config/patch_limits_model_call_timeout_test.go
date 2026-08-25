package config

import "testing"

// TestApplyLimitsPatchModelCallTimeout pins that the model_call_timeout patch
// field is actually applied. It was added once without being wired into
// applyLimitsPatch, which silently made the config knob a no-op.
func TestApplyLimitsPatchModelCallTimeout(t *testing.T) {
	tenMinutes := MustDuration("10m")
	thirtySeconds := MustDuration("30s")
	zero := MustDuration("0s")

	for _, tt := range []struct {
		name    string
		initial LimitsConfig
		patch   limitsPatch
		want    Duration
	}{
		{
			name:    "overrides the existing value",
			initial: LimitsConfig{ModelCallTimeout: tenMinutes},
			patch:   limitsPatch{ModelCallTimeout: &thirtySeconds},
			want:    thirtySeconds,
		},
		{
			name:    "absent patch leaves the value untouched",
			initial: LimitsConfig{ModelCallTimeout: tenMinutes},
			patch:   limitsPatch{},
			want:    tenMinutes,
		},
		{
			name:    "explicit zero disables the timeout",
			initial: LimitsConfig{ModelCallTimeout: tenMinutes},
			patch:   limitsPatch{ModelCallTimeout: &zero},
			want:    zero,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.initial
			applyLimitsPatch(&got, &tt.patch)
			if got.ModelCallTimeout.Duration() != tt.want.Duration() {
				t.Errorf("ModelCallTimeout = %v, want %v", got.ModelCallTimeout.Duration(), tt.want.Duration())
			}
		})
	}
}
