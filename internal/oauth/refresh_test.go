package oauth

import (
	"testing"

	"golang.org/x/oauth2"
)

// structuredToken returns a token carrying the metadata keys Steiner preserves
// across a refresh.
func structuredToken(id, account, apiKey string) *oauth2.Token {
	return (&oauth2.Token{}).WithExtra(map[string]any{
		"id_token":               id,
		chatGPTAccountIDExtraKey: account,
		OpenAIAPIKeyExtraKey:     apiKey,
	})
}

func TestPreserveTokenMetadata(t *testing.T) {
	tests := []struct {
		name     string
		token    *oauth2.Token
		previous *oauth2.Token
		want     map[string]string
	}{
		{
			name:     "response values win",
			token:    structuredToken("fresh-id", "fresh-account", "fresh-api-key"),
			previous: structuredToken("previous-id", "previous-account", "previous-api-key"),
			want: map[string]string{
				"id_token":               "fresh-id",
				chatGPTAccountIDExtraKey: "fresh-account",
				OpenAIAPIKeyExtraKey:     "fresh-api-key",
			},
		},
		{
			name:     "values the response omits are kept",
			token:    structuredToken("", "", ""),
			previous: structuredToken("previous-id", "previous-account", "previous-api-key"),
			want: map[string]string{
				"id_token":               "previous-id",
				chatGPTAccountIDExtraKey: "previous-account",
				OpenAIAPIKeyExtraKey:     "previous-api-key",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged := preserveTokenMetadata(tt.token, tt.previous)
			for key, want := range tt.want {
				if got := tokenExtraString(merged, key); got != want {
					t.Errorf("Extra(%q) = %q, want %q", key, got, want)
				}
			}
		})
	}
}
