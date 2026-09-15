package metadata

import "testing"

func TestParseIndex_Valid(t *testing.T) {
	data := []byte(`{"openai":{"models":{"gpt-4o":{"limit":{"context":128000,"output":16384}}}}}`)
	idx := ParseIndex(data)
	if idx.malformed {
		t.Fatal("malformed = true, want false")
	}
	provider, ok := idx.providers["openai"]
	if !ok {
		t.Fatal("provider \"openai\" not indexed")
	}
	model, ok := provider.models["gpt-4o"]
	if !ok || model.malformed {
		t.Fatalf("model = %+v, ok = %v, want a valid indexed model", model, ok)
	}
	if model.info.ContextWindow != 128000 {
		t.Errorf("ContextWindow = %d, want 128000", model.info.ContextWindow)
	}
}

func TestParseIndex_MalformedTopLevel(t *testing.T) {
	idx := ParseIndex([]byte(`not json`))
	if !idx.malformed {
		t.Fatal("malformed = false, want true")
	}
	result := idx.LookupProvider("openai", "gpt-4o")
	if result.Reason != LookupReasonMalformed {
		t.Fatalf("Reason = %q, want %q", result.Reason, LookupReasonMalformed)
	}
	// Every LookupProvider call on a malformed Index reports malformed,
	// regardless of providerID/modelID.
	result = idx.LookupProvider("", "")
	if result.Reason != LookupReasonMalformed {
		t.Fatalf("Reason = %q, want %q", result.Reason, LookupReasonMalformed)
	}
}

func TestParseIndex_MalformedModelEntryMixedWithValid(t *testing.T) {
	data := []byte(`{
		"openai":{"models":{
			"gpt-4o":{"limit":{"context":128000}},
			"bad-model":[]
		}}
	}`)
	idx := ParseIndex(data)
	if idx.malformed {
		t.Fatal("malformed = true, want false (top-level document is valid)")
	}

	good, ok := idx.providers["openai"].models["gpt-4o"]
	if !ok || good.malformed {
		t.Fatalf("gpt-4o = %+v, ok = %v, want a valid indexed model", good, ok)
	}
	bad, ok := idx.providers["openai"].models["bad-model"]
	if !ok || !bad.malformed {
		t.Fatalf("bad-model = %+v, ok = %v, want a malformed indexed model", bad, ok)
	}

	if result := idx.LookupProvider("openai", "gpt-4o"); result.Reason != "" || !result.Info.Found {
		t.Fatalf("LookupProvider(gpt-4o) = %+v, want a found match", result)
	}
	if result := idx.LookupProvider("openai", "bad-model"); result.Reason != LookupReasonMalformed {
		t.Fatalf("Reason = %q, want %q", result.Reason, LookupReasonMalformed)
	}
}

func TestParseIndex_NullModelEntry(t *testing.T) {
	idx := ParseIndex([]byte(`{"openai":{"models":{"gpt-4o":null}}}`))
	model, ok := idx.providers["openai"].models["gpt-4o"]
	if !ok || !model.malformed {
		t.Fatalf("model = %+v, ok = %v, want a malformed (null) indexed model", model, ok)
	}
	if result := idx.LookupProvider("openai", "gpt-4o"); result.Reason != LookupReasonMalformed {
		t.Fatalf("Reason = %q, want %q", result.Reason, LookupReasonMalformed)
	}
}

func TestParseIndex_MalformedProviderObjectIsSkippedNotMalformed(t *testing.T) {
	// A provider whose value doesn't unmarshal into the expected shape, or
	// lacks a "models" key, contributes zero models and does not mark the
	// top-level Index malformed.
	idx := ParseIndex([]byte(`{"openai":42,"anthropic":{"id":"anthropic"}}`))
	if idx.malformed {
		t.Fatal("malformed = true, want false")
	}
	if _, ok := idx.providers["openai"]; ok {
		t.Fatal("expected \"openai\" to be skipped, not indexed")
	}
	if _, ok := idx.providers["anthropic"]; ok {
		t.Fatal("expected \"anthropic\" to be skipped (no models key), not indexed")
	}
	result := idx.LookupProvider("openai", "gpt-4o")
	if result.Reason != LookupReasonNotFound {
		t.Fatalf("Reason = %q, want %q", result.Reason, LookupReasonNotFound)
	}
}

func TestIndexLookupProvider_Found(t *testing.T) {
	data := []byte(`{"openai":{"models":{"gpt-4o":{"limit":{"context":128000,"output":16384}}}}}`)
	info := ParseIndex(data).LookupProvider("openai", "gpt-4o").Info
	if info.ContextWindow != 128000 {
		t.Errorf("ContextWindow: got %d, want 128000", info.ContextWindow)
	}
	if info.MaxOutputTokens != 16384 {
		t.Errorf("MaxOutputTokens: got %d, want 16384", info.MaxOutputTokens)
	}
}

func TestIndexLookupProvider_NotFound(t *testing.T) {
	data := []byte(`{"openai":{"models":{"gpt-4o":{"limit":{"context":128000,"output":16384}}}}}`)
	info := ParseIndex(data).LookupProvider("openai", "unknown-model").Info
	if info.ContextWindow != 0 || info.MaxOutputTokens != 0 {
		t.Errorf("expected zero ModelInfo, got %+v", info)
	}
}

func TestIndexLookupProvider_MalformedJSON(t *testing.T) {
	info := ParseIndex([]byte(`not json`)).LookupProvider("openai", "gpt-4o").Info
	if info.ContextWindow != 0 || info.MaxOutputTokens != 0 {
		t.Errorf("expected zero ModelInfo on malformed JSON, got %+v", info)
	}
}

func TestIndexLookupProvider_MalformedModelEntry(t *testing.T) {
	result := ParseIndex([]byte(`{"openai":{"models":{"gpt-4o":[]}}}`)).LookupProvider("openai", "gpt-4o")
	if result.Reason != LookupReasonMalformed {
		t.Fatalf("Reason = %q, want %q", result.Reason, LookupReasonMalformed)
	}
}

func TestIndexLookupProvider_NullModelEntryIsMalformed(t *testing.T) {
	result := ParseIndex([]byte(`{"openai":{"models":{"gpt-4o":null}}}`)).LookupProvider("openai", "gpt-4o")
	if result.Reason != LookupReasonMalformed {
		t.Fatalf("Reason = %q, want %q", result.Reason, LookupReasonMalformed)
	}
}

func TestIndexLookupProvider_ValidMatchTakesPrecedenceOverMalformedOtherProvider(t *testing.T) {
	data := []byte(`{
		"bad":{"models":{"gpt-4o":null}},
		"openai":{"models":{"gpt-4o":{"limit":{"context":128000}}}}
	}`)
	result := ParseIndex(data).LookupProvider("local", "gpt-4o")
	if result.Reason != LookupReasonProviderMismatch {
		t.Fatalf("Reason = %q, want %q", result.Reason, LookupReasonProviderMismatch)
	}
}

func TestIndexLookupProvider_MissingModelsKey(t *testing.T) {
	data := []byte(`{"openai":{"id":"openai"}}`)
	info := ParseIndex(data).LookupProvider("openai", "gpt-4o").Info
	if info.ContextWindow != 0 || info.MaxOutputTokens != 0 {
		t.Errorf("expected zero ModelInfo for missing 'models' key, got %+v", info)
	}
}

func TestIndexLookupProvider_PartialFields(t *testing.T) {
	data := []byte(`{"anthropic":{"models":{"claude-3":{"limit":{"context":200000}}}}}`)
	info := ParseIndex(data).LookupProvider("anthropic", "claude-3").Info
	if info.ContextWindow != 200000 {
		t.Errorf("ContextWindow: got %d, want 200000", info.ContextWindow)
	}
	if info.MaxOutputTokens != 0 {
		t.Errorf("expected zero MaxOutputTokens, got %d", info.MaxOutputTokens)
	}
}

func TestIndexLookupProvider_EmptyData(t *testing.T) {
	info := ParseIndex([]byte{}).LookupProvider("openai", "gpt-4o").Info
	if info.ContextWindow != 0 || info.MaxOutputTokens != 0 {
		t.Errorf("expected zero ModelInfo for empty data, got %+v", info)
	}
}

func TestIndexLookupProvider_PrefersProviderSpecificLimits(t *testing.T) {
	data := []byte(`{
		"opencode-go":{"models":{"deepseek-v4-flash":{"limit":{"context":1000000,"output":384000}}}},
		"ollama-cloud":{"models":{"deepseek-v4-flash":{"limit":{"context":1048576,"output":1048576}}}}
	}`)
	info := ParseIndex(data).LookupProvider("opencode-go", "deepseek-v4-flash").Info
	if info.ContextWindow != 1000000 {
		t.Errorf("ContextWindow: got %d, want 1000000", info.ContextWindow)
	}
	if info.MaxOutputTokens != 384000 {
		t.Errorf("MaxOutputTokens: got %d, want 384000", info.MaxOutputTokens)
	}
}

func TestIndexLookupProvider_RejectsProviderMismatch(t *testing.T) {
	data := []byte(`{
		"opencode-go":{"models":{"deepseek-v4-flash":{"limit":{"context":1000000,"output":384000}}}}
	}`)
	result := ParseIndex(data).LookupProvider("local", "deepseek-v4-flash")
	if result.Info.Found {
		t.Fatalf("expected no metadata, got %+v", result.Info)
	}
	if result.Reason != LookupReasonProviderMismatch {
		t.Fatalf("Reason = %q, want %q", result.Reason, LookupReasonProviderMismatch)
	}
}

func TestIndexLookupProvider_IncludesTransportMetadata(t *testing.T) {
	data := []byte(`{
		"opencode-go":{
			"npm":"@ai-sdk/openai-compatible",
			"api":"https://opencode.ai/zen/go/v1/",
			"models":{
				"minimax-m3":{
					"limit":{"context":256000,"output":8192},
					"provider":{"npm":"@ai-sdk/anthropic","api":"https://api.minimax.chat/anthropic"},
					"interleaved":{"field":"reasoning_content"}
				},
				"kimi-k2.6":{
					"provider":{"npm":"@ai-sdk/openai-compatible","api":"https://api.moonshot.ai/v1"}
				}
			}
		}
	}`)

	idx := ParseIndex(data)

	t.Run("model-level provider metadata wins", func(t *testing.T) {
		info := idx.LookupProvider("opencode-go", "minimax-m3").Info
		if got, want := info.ProviderNPM, "@ai-sdk/openai-compatible"; got != want {
			t.Fatalf("ProviderNPM = %q, want %q", got, want)
		}
		if got, want := info.ProviderAPI, "https://opencode.ai/zen/go/v1/"; got != want {
			t.Fatalf("ProviderAPI = %q, want %q", got, want)
		}
		if got, want := info.ModelProviderNPM, "@ai-sdk/anthropic"; got != want {
			t.Fatalf("ModelProviderNPM = %q, want %q", got, want)
		}
		if got, want := info.ModelProviderAPI, "https://api.minimax.chat/anthropic"; got != want {
			t.Fatalf("ModelProviderAPI = %q, want %q", got, want)
		}
		if got, want := info.InterleavedField, "reasoning_content"; got != want {
			t.Fatalf("InterleavedField = %q, want %q", got, want)
		}
		if !info.ReasoningEchoBack {
			t.Fatal("ReasoningEchoBack = false, want true")
		}
	})

	t.Run("provider-level metadata is preserved without model override", func(t *testing.T) {
		info := idx.LookupProvider("opencode-go", "kimi-k2.6").Info
		if got, want := info.ProviderNPM, "@ai-sdk/openai-compatible"; got != want {
			t.Fatalf("ProviderNPM = %q, want %q", got, want)
		}
		if got, want := info.ModelProviderNPM, "@ai-sdk/openai-compatible"; got != want {
			t.Fatalf("ModelProviderNPM = %q, want %q", got, want)
		}
		if got, want := info.ModelProviderAPI, "https://api.moonshot.ai/v1"; got != want {
			t.Fatalf("ModelProviderAPI = %q, want %q", got, want)
		}
	})
}

func TestIndexLookupProvider_ReasoningEchoBack(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		providerID string
		modelID    string
		want       bool
	}{
		{
			name:       "interleaved field is reasoning_content",
			data:       []byte(`{"deepseek":{"models":{"deepseek-r1":{"limit":{"context":128000},"interleaved":{"field":"reasoning_content"}}}}}`),
			providerID: "deepseek",
			modelID:    "deepseek-r1",
			want:       true,
		},
		{
			name:       "interleaved field is something else",
			data:       []byte(`{"prov":{"models":{"model-x":{"limit":{"context":128000},"interleaved":{"field":"other"}}}}}`),
			providerID: "prov",
			modelID:    "model-x",
			want:       false,
		},
		{
			name:       "no interleaved key",
			data:       []byte(`{"openai":{"models":{"gpt-4o":{"limit":{"context":128000}}}}}`),
			providerID: "openai",
			modelID:    "gpt-4o",
			want:       false,
		},
		{
			name:       "interleaved without field",
			data:       []byte(`{"prov":{"models":{"model-y":{"limit":{"context":128000},"interleaved":{}}}}}`),
			providerID: "prov",
			modelID:    "model-y",
			want:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ParseIndex(tt.data).LookupProvider(tt.providerID, tt.modelID).Info
			if info.ReasoningEchoBack != tt.want {
				t.Errorf("ReasoningEchoBack=%v, want %v", info.ReasoningEchoBack, tt.want)
			}
		})
	}
}

func TestIndexLookupProvider_VisionInput(t *testing.T) {
	tests := []struct {
		name            string
		data            []byte
		providerID      string
		modelID         string
		wantFound       bool
		wantVisionInput bool
	}{
		{
			name:            "image modality present",
			data:            []byte(`{"prov":{"models":{"gpt-4v":{"modalities":{"input":["text","image"]}}}}}`),
			providerID:      "prov",
			modelID:         "gpt-4v",
			wantFound:       true,
			wantVisionInput: true,
		},
		{
			name:            "image modality absent",
			data:            []byte(`{"prov":{"models":{"deepseek":{"modalities":{"input":["text"]}}}}}`),
			providerID:      "prov",
			modelID:         "deepseek",
			wantFound:       true,
			wantVisionInput: false,
		},
		{
			name:            "model not found in dataset",
			data:            []byte(`{"prov":{"models":{"gpt-4o":{"limit":{"context":128000}}}}}`),
			providerID:      "prov",
			modelID:         "unknown-model",
			wantFound:       false,
			wantVisionInput: false,
		},
		{
			name:            "case-insensitive image modality matching",
			data:            []byte(`{"prov":{"models":{"model-x":{"modalities":{"input":["text","IMAGE"]}}}}}`),
			providerID:      "prov",
			modelID:         "model-x",
			wantFound:       true,
			wantVisionInput: true,
		},
		{
			name:            "no modalities field",
			data:            []byte(`{"prov":{"models":{"model-y":{"limit":{"context":128000}}}}}`),
			providerID:      "prov",
			modelID:         "model-y",
			wantFound:       true,
			wantVisionInput: false,
		},
		{
			name:            "empty modalities input array",
			data:            []byte(`{"prov":{"models":{"model-z":{"modalities":{"input":[]}}}}}`),
			providerID:      "prov",
			modelID:         "model-z",
			wantFound:       true,
			wantVisionInput: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ParseIndex(tt.data).LookupProvider(tt.providerID, tt.modelID).Info
			if info.Found != tt.wantFound {
				t.Errorf("Found=%v, want %v", info.Found, tt.wantFound)
			}
			if info.VisionInput != tt.wantVisionInput {
				t.Errorf("VisionInput=%v, want %v", info.VisionInput, tt.wantVisionInput)
			}
		})
	}
}

func TestIndexLookupProvider_ReasoningSupportedEfforts(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		providerID string
		modelID    string
		want       []string
	}{
		{
			name:       "reasoning_options with effort type",
			data:       []byte(`{"openai":{"models":{"gpt-5.4-mini":{"limit":{"context":200000,"output":128000},"reasoning_options":[{"type":"effort","values":["none","low","medium","high","xhigh"]}]}}}}`),
			providerID: "openai",
			modelID:    "gpt-5.4-mini",
			want:       []string{"none", "low", "medium", "high", "xhigh"},
		},
		{
			name:       "no reasoning_options returns nil",
			data:       []byte(`{"openai":{"models":{"gpt-4o":{"limit":{"context":128000}}}}}`),
			providerID: "openai",
			modelID:    "gpt-4o",
			want:       nil,
		},
		{
			name:       "reasoning_options without effort type returns nil",
			data:       []byte(`{"openai":{"models":{"o3":{"limit":{"context":200000},"reasoning_options":[{"type":"budget_tokens","values":["1000","2000"]}]}}}}`),
			providerID: "openai",
			modelID:    "o3",
			want:       nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ParseIndex(tt.data).LookupProvider(tt.providerID, tt.modelID).Info
			if !equalStringSlice(info.ReasoningSupportedEfforts, tt.want) {
				t.Errorf("ReasoningSupportedEfforts=%v, want %v", info.ReasoningSupportedEfforts, tt.want)
			}
		})
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
