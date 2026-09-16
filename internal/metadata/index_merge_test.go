package metadata

import "testing"

func TestLookupMerged_MinLimits(t *testing.T) {
	data := []byte(`{
		"openai":{"models":{"gpt-5.6-luna":{"limit":{"context":1050000,"output":64000}}}},
		"abacus":{"models":{"gpt-5.6-luna":{"limit":{"context":1000000,"output":32000}}}}
	}`)
	info := ParseIndex(data).LookupMerged("gpt-5.6-luna").Info
	if info.ContextWindow != 1000000 {
		t.Errorf("ContextWindow = %d, want 1000000", info.ContextWindow)
	}
	if info.MaxOutputTokens != 32000 {
		t.Errorf("MaxOutputTokens = %d, want 32000", info.MaxOutputTokens)
	}
}

func TestLookupMerged_ZeroValuesIgnoredInMin(t *testing.T) {
	data := []byte(`{
		"a":{"models":{"m":{"limit":{"context":0,"output":0}}}},
		"b":{"models":{"m":{"limit":{"context":500000,"output":16000}}}}
	}`)
	info := ParseIndex(data).LookupMerged("m").Info
	if info.ContextWindow != 500000 {
		t.Errorf("ContextWindow = %d, want 500000", info.ContextWindow)
	}
	if info.MaxOutputTokens != 16000 {
		t.Errorf("MaxOutputTokens = %d, want 16000", info.MaxOutputTokens)
	}
}

func TestLookupMerged_VisionAndBooleans(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "all vision true",
			data: []byte(`{
				"a":{"models":{"m":{"modalities":{"input":["text","image"]}}}},
				"b":{"models":{"m":{"modalities":{"input":["text","image"]}}}}
			}`),
			want: true,
		},
		{
			name: "mixed vision",
			data: []byte(`{
				"a":{"models":{"m":{"modalities":{"input":["text","image"]}}}},
				"b":{"models":{"m":{"modalities":{"input":["text"]}}}}
			}`),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ParseIndex(tt.data).LookupMerged("m").Info
			if info.VisionInput != tt.want {
				t.Errorf("VisionInput = %v, want %v", info.VisionInput, tt.want)
			}
		})
	}
}

func TestLookupMerged_EffortsIntersection(t *testing.T) {
	data := []byte(`{
		"a":{"models":{"m":{"reasoning_options":[{"type":"effort","values":["low","medium","high"]}]}}},
		"b":{"models":{"m":{"reasoning_options":[{"type":"effort","values":["medium","high","xhigh"]}]}}}
	}`)
	info := ParseIndex(data).LookupMerged("m").Info
	want := []string{"medium", "high"}
	if !equalStringSlice(info.ReasoningSupportedEfforts, want) {
		t.Errorf("ReasoningSupportedEfforts = %v, want %v", info.ReasoningSupportedEfforts, want)
	}
}

func TestLookupMerged_EffortsNilWhenAnyEntryEmpty(t *testing.T) {
	data := []byte(`{
		"a":{"models":{"m":{"reasoning_options":[{"type":"effort","values":["low","medium"]}]}}},
		"b":{"models":{"m":{"limit":{"context":1000}}}}
	}`)
	info := ParseIndex(data).LookupMerged("m").Info
	if info.ReasoningSupportedEfforts != nil {
		t.Errorf("ReasoningSupportedEfforts = %v, want nil", info.ReasoningSupportedEfforts)
	}
}

func TestLookupMerged_NPMAPICleared(t *testing.T) {
	data := []byte(`{
		"a":{"npm":"@ai-sdk/openai","api":"https://a.example","models":{"m":{"limit":{"context":1000}}}},
		"b":{"npm":"@ai-sdk/anthropic","api":"https://b.example","models":{"m":{"limit":{"context":2000}}}}
	}`)
	info := ParseIndex(data).LookupMerged("m").Info
	if info.ProviderNPM != "" || info.ProviderAPI != "" || info.ModelProviderNPM != "" || info.ModelProviderAPI != "" {
		t.Errorf("expected all npm/api fields cleared, got %+v", info)
	}
	if info.ProviderCount != 2 {
		t.Errorf("ProviderCount = %d, want 2", info.ProviderCount)
	}
	result := ParseIndex(data).LookupMerged("m")
	if result.Reason != LookupReasonMerged {
		t.Errorf("Reason = %q, want %q", result.Reason, LookupReasonMerged)
	}
}

func TestLookupMerged_SingleEntryKeepsProvenance(t *testing.T) {
	data := []byte(`{
		"a":{"npm":"@ai-sdk/openai","api":"https://a.example","models":{"m":{"limit":{"context":1000}}}}
	}`)
	result := ParseIndex(data).LookupMerged("m")
	if result.Reason != "" {
		t.Errorf("Reason = %q, want empty", result.Reason)
	}
	if result.Info.ProviderNPM != "@ai-sdk/openai" {
		t.Errorf("ProviderNPM = %q, want @ai-sdk/openai", result.Info.ProviderNPM)
	}
	if result.Info.ProviderCount != 1 {
		t.Errorf("ProviderCount = %d, want 1", result.Info.ProviderCount)
	}
}

func TestLookupMerged_MalformedIgnoredWhenValidExists(t *testing.T) {
	data := []byte(`{
		"bad":{"models":{"m":null}},
		"good":{"models":{"m":{"limit":{"context":1000}}}}
	}`)
	result := ParseIndex(data).LookupMerged("m")
	if result.Reason != "" {
		t.Errorf("Reason = %q, want empty", result.Reason)
	}
	if result.Info.ContextWindow != 1000 {
		t.Errorf("ContextWindow = %d, want 1000", result.Info.ContextWindow)
	}
	if result.Info.ProviderCount != 1 {
		t.Errorf("ProviderCount = %d, want 1", result.Info.ProviderCount)
	}
}

func TestLookupMerged_AllMalformed(t *testing.T) {
	data := []byte(`{"a":{"models":{"m":null}},"b":{"models":{"m":[]}}}`)
	result := ParseIndex(data).LookupMerged("m")
	if result.Reason != LookupReasonMalformed {
		t.Errorf("Reason = %q, want %q", result.Reason, LookupReasonMalformed)
	}
}

func TestLookupMerged_NotFound(t *testing.T) {
	data := []byte(`{"a":{"models":{"other":{"limit":{"context":1000}}}}}`)
	result := ParseIndex(data).LookupMerged("m")
	if result.Reason != LookupReasonNotFound {
		t.Errorf("Reason = %q, want %q", result.Reason, LookupReasonNotFound)
	}
}

func TestLookupMerged_MalformedTopLevel(t *testing.T) {
	result := ParseIndex([]byte(`not json`)).LookupMerged("m")
	if result.Reason != LookupReasonMalformed {
		t.Errorf("Reason = %q, want %q", result.Reason, LookupReasonMalformed)
	}
}

func TestLookupMerged_Deterministic(t *testing.T) {
	data := []byte(`{
		"zzz":{"models":{"m":{"limit":{"context":3000,"output":300},"reasoning_options":[{"type":"effort","values":["low","high"]}]}}},
		"aaa":{"models":{"m":{"limit":{"context":1000,"output":100},"reasoning_options":[{"type":"effort","values":["high","low"]}]}}},
		"mmm":{"models":{"m":{"limit":{"context":2000,"output":200},"reasoning_options":[{"type":"effort","values":["low","medium","high"]}]}}}
	}`)
	idx := ParseIndex(data)
	first := idx.LookupMerged("m")
	for i := 0; i < 50; i++ {
		got := idx.LookupMerged("m")
		if got.Reason != first.Reason || !equalStringSlice(got.Info.ReasoningSupportedEfforts, first.Info.ReasoningSupportedEfforts) ||
			got.Info.ContextWindow != first.Info.ContextWindow || got.Info.MaxOutputTokens != first.Info.MaxOutputTokens {
			t.Fatalf("iteration %d: got %+v, want %+v", i, got, first)
		}
	}

	// Same data, keys in different textual order, must produce identical output.
	dataReordered := []byte(`{
		"mmm":{"models":{"m":{"limit":{"context":2000,"output":200},"reasoning_options":[{"type":"effort","values":["low","medium","high"]}]}}},
		"aaa":{"models":{"m":{"limit":{"context":1000,"output":100},"reasoning_options":[{"type":"effort","values":["high","low"]}]}}},
		"zzz":{"models":{"m":{"limit":{"context":3000,"output":300},"reasoning_options":[{"type":"effort","values":["low","high"]}]}}}
	}`)
	reordered := ParseIndex(dataReordered).LookupMerged("m")
	if reordered.Reason != first.Reason || !equalStringSlice(reordered.Info.ReasoningSupportedEfforts, first.Info.ReasoningSupportedEfforts) ||
		reordered.Info.ContextWindow != first.Info.ContextWindow || reordered.Info.MaxOutputTokens != first.Info.MaxOutputTokens {
		t.Fatalf("reordered input produced different output: got %+v, want %+v", reordered, first)
	}
}
