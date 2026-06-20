package agent

import (
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestApplyPromptSuffix(t *testing.T) {
	msg := provider.Message{Role: provider.MessageRoleUser, Content: "hello"}
	msgWithSuffix := provider.Message{Role: provider.MessageRoleUser, Content: "hello <|think_off|>"}

	cases := []struct {
		name           string
		suffix         string
		req            provider.ChatRequest
		wantMessages   []provider.Message
		wantExtraParam map[string]any
	}{
		{
			name:         "empty suffix leaves request unchanged",
			req:          provider.ChatRequest{Messages: []provider.Message{msg}},
			wantMessages: []provider.Message{msg},
		},
		{
			name:         "suffix appended to last user message",
			suffix:       "<|think_off|>",
			req:          provider.ChatRequest{Messages: []provider.Message{msg}},
			wantMessages: []provider.Message{msgWithSuffix},
		},
		{
			name:   "suffix is not duplicated",
			suffix: "<|think_off|>",
			req: provider.ChatRequest{Messages: []provider.Message{
				msgWithSuffix,
			}},
			wantMessages: []provider.Message{msgWithSuffix},
		},
		{
			name:   "only last user message controls duplication",
			suffix: "<|think_off|>",
			req: provider.ChatRequest{Messages: []provider.Message{
				msgWithSuffix,
				{Role: provider.MessageRoleAssistant, Content: "response"},
				msg,
			}},
			wantMessages: []provider.Message{
				msgWithSuffix,
				{Role: provider.MessageRoleAssistant, Content: "response"},
				msgWithSuffix,
			},
		},
		{
			name:   "extra params pass through unchanged",
			suffix: "<|think_off|>",
			req: provider.ChatRequest{
				Messages:    []provider.Message{msg},
				Params:      map[string]any{"temperature": 0.5},
				ExtraParams: map[string]any{"reasoning": map[string]any{"effort": "medium"}},
			},
			wantMessages: []provider.Message{msgWithSuffix},
			wantExtraParam: map[string]any{
				"reasoning": map[string]any{"effort": "medium"},
			},
		},
		{
			name:         "no user message leaves request unchanged",
			suffix:       "<|think_off|>",
			req:          provider.ChatRequest{Messages: []provider.Message{{Role: provider.MessageRoleSystem, Content: "system"}}},
			wantMessages: []provider.Message{{Role: provider.MessageRoleSystem, Content: "system"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := applyPromptSuffix(tc.suffix, tc.req)
			if !reflect.DeepEqual(got.Messages, tc.wantMessages) {
				t.Fatalf("Messages = %#v, want %#v", got.Messages, tc.wantMessages)
			}
			if tc.wantExtraParam != nil && !reflect.DeepEqual(got.ExtraParams, tc.wantExtraParam) {
				t.Fatalf("ExtraParams = %#v, want %#v", got.ExtraParams, tc.wantExtraParam)
			}
			if !reflect.DeepEqual(got.Params, tc.req.Params) {
				t.Fatalf("Params = %#v, want unchanged %#v", got.Params, tc.req.Params)
			}
		})
	}
}

func TestApplyPromptSuffixUsedForTurnChatRequest(t *testing.T) {
	req := RunRequest{
		ResolvedModel: provider.ResolvedModel{PromptSuffix: "<|think_off|>"},
	}
	chatReq := provider.ChatRequest{
		Messages:    []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		ExtraParams: map[string]any{"reasoning": map[string]any{"effort": "medium"}},
	}

	got := applyPromptSuffix(req.ResolvedModel.PromptSuffix, chatReq)
	if got.Messages[0].Content != "hello <|think_off|>" {
		t.Fatalf("message content = %q, want suffix appended", got.Messages[0].Content)
	}
	if !reflect.DeepEqual(got.ExtraParams, chatReq.ExtraParams) {
		t.Fatalf("ExtraParams = %#v, want unchanged %#v", got.ExtraParams, chatReq.ExtraParams)
	}
}
