package agent

import (
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestHasThinkingMarker(t *testing.T) {
	cases := []struct {
		name      string
		messages  []provider.Message
		marker    string
		wantFound bool
	}{
		{
			name: "marker in last user message",
			messages: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "hello <|think_off|> world"},
			},
			marker:    "<|think_off|>",
			wantFound: true,
		},
		{
			name: "marker not present",
			messages: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "hello world"},
			},
			marker:    "<|think_off|>",
			wantFound: false,
		},
		{
			name: "marker only in older message not last",
			messages: []provider.Message{
				{Role: provider.MessageRoleUser, Content: "first <|think_off|>"},
				{Role: provider.MessageRoleAssistant, Content: "response"},
				{Role: provider.MessageRoleUser, Content: "second"},
			},
			marker:    "<|think_off|>",
			wantFound: false,
		},
		{
			name:      "empty messages",
			messages:  []provider.Message{},
			marker:    "<|think_off|>",
			wantFound: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hasThinkingMarker(tc.messages, tc.marker)
			if got != tc.wantFound {
				t.Errorf("got %v, want %v", got, tc.wantFound)
			}
		})
	}
}

func TestMergeThinkingParams(t *testing.T) {
	cases := []struct {
		name   string
		base   map[string]any
		params map[string]any
		want   map[string]any
	}{
		{
			name:   "params wins on collision",
			base:   map[string]any{"k": "base"},
			params: map[string]any{"k": "params"},
			want:   map[string]any{"k": "params"},
		},
		{
			name:   "empty base",
			base:   nil,
			params: map[string]any{"thinking": "on"},
			want:   map[string]any{"thinking": "on"},
		},
		{
			name:   "empty params",
			base:   map[string]any{"x": 1},
			params: nil,
			want:   map[string]any{"x": 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeThinkingParams(tc.base, tc.params)
			for k, wv := range tc.want {
				if gv, ok := got[k]; !ok || gv != wv {
					t.Errorf("key %q: got %v, want %v", k, gv, wv)
				}
			}
		})
	}
}

func TestApplyThinking(t *testing.T) {
	params := map[string]any{"thinking": "on"}
	msg := provider.Message{Role: provider.MessageRoleUser, Content: "hello"}
	msgWithMarker := provider.Message{Role: provider.MessageRoleUser, Content: "hello <|off|>"}

	cases := []struct {
		name            string
		cfg             thinkingCfg
		req             provider.ChatRequest
		wantExtraParams map[string]any
		wantMsgContent  string
	}{
		{
			name:            "enabled false, no marker configured → no change",
			cfg:             thinkingCfg{enabled: false, params: params},
			req:             provider.ChatRequest{Messages: []provider.Message{msg}},
			wantExtraParams: nil,
			wantMsgContent:  "hello",
		},
		{
			name:           "enabled false, marker configured → marker appended",
			cfg:            thinkingCfg{enabled: false, params: params, disableMarker: "<|off|>"},
			req:            provider.ChatRequest{Messages: []provider.Message{msg}},
			wantMsgContent: "hello <|off|>",
		},
		{
			name:           "enabled false, marker already present → not duplicated",
			cfg:            thinkingCfg{enabled: false, params: params, disableMarker: "<|off|>"},
			req:            provider.ChatRequest{Messages: []provider.Message{msgWithMarker}},
			wantMsgContent: "hello <|off|>",
		},
		{
			name:            "enabled true, nil params → no change",
			cfg:             thinkingCfg{enabled: true, params: nil},
			req:             provider.ChatRequest{Messages: []provider.Message{msg}},
			wantExtraParams: nil,
			wantMsgContent:  "hello",
		},
		{
			name:            "enabled true, params set, no marker → params merged",
			cfg:             thinkingCfg{enabled: true, params: params},
			req:             provider.ChatRequest{Messages: []provider.Message{msg}},
			wantExtraParams: params,
			wantMsgContent:  "hello",
		},
		{
			name:            "marker present → no params injected, message untouched",
			cfg:             thinkingCfg{enabled: true, params: params, disableMarker: "<|off|>"},
			req:             provider.ChatRequest{Messages: []provider.Message{msgWithMarker}},
			wantExtraParams: nil,
			wantMsgContent:  "hello <|off|>",
		},
		{
			name:            "marker not found → params merged",
			cfg:             thinkingCfg{enabled: true, params: params, disableMarker: "<|off|>"},
			req:             provider.ChatRequest{Messages: []provider.Message{msg}},
			wantExtraParams: params,
			wantMsgContent:  "hello",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := applyThinking(tc.cfg, tc.req)
			if tc.wantExtraParams == nil && got.ExtraParams != nil {
				t.Errorf("ExtraParams: got %v, want nil", got.ExtraParams)
			}
			if tc.wantExtraParams != nil {
				for k, wv := range tc.wantExtraParams {
					if gv, ok := got.ExtraParams[k]; !ok || gv != wv {
						t.Errorf("ExtraParams[%q]: got %v, want %v", k, gv, wv)
					}
				}
			}
			if len(got.Messages) > 0 {
				last := got.Messages[len(got.Messages)-1]
				if last.Role == provider.MessageRoleUser && last.Content != tc.wantMsgContent {
					t.Errorf("last msg content: got %q, want %q", last.Content, tc.wantMsgContent)
				}
			}
		})
	}
}
