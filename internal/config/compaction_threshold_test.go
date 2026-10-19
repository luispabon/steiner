package config

import "testing"

func TestCompactionThresholdConfigPatchAndValidation(t *testing.T) {
	cfg := validBase()
	model := cfg.Models.Definitions["default"]
	applyModelPatch(&model, &modelPatch{Advanced: &advancedPatch{Limits: &advancedLimitsPatch{CompactionThresholdTokens: intPtr(12000)}}})
	if got, want := model.Advanced.Limits.CompactionThresholdTokens, 12000; got != want {
		t.Fatalf("CompactionThresholdTokens = %d, want %d", got, want)
	}
	if !model.Advanced.Limits.CompactionThresholdTokensExplicit() {
		t.Fatal("CompactionThresholdTokensExplicit() = false, want true")
	}
	cfg.Models.Definitions["default"] = model
	if err := validate(cfg, ""); err != nil {
		t.Fatalf("validate() with positive threshold = %v, want nil", err)
	}

	for _, value := range []int{0, -1} {
		t.Run("invalid", func(t *testing.T) {
			invalid := cfg.Models.Definitions["default"]
			invalid.Advanced.Limits.CompactionThresholdTokens = value
			invalid.Advanced.Limits.compactionThresholdSet = true
			cfg.Models.Definitions["default"] = invalid
			err := validate(cfg, "")
			if err == nil || err.Error() == "" {
				t.Fatalf("validate() with threshold %d = nil, want error", value)
			}
		})
	}
}

func TestCompactionThresholdAbsentRemainsUnconfigured(t *testing.T) {
	limits := AdvancedLimitsConfig{}
	if limits.CompactionThresholdTokensExplicit() {
		t.Fatal("CompactionThresholdTokensExplicit() = true for absent field")
	}
}
