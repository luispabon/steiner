package oauth

import (
	"time"

	"golang.org/x/oauth2"
)

const proactiveRefreshWindow = 5 * time.Minute

func preserveTokenMetadata(token, previous *oauth2.Token) *oauth2.Token {
	extra := make(map[string]any)
	for _, key := range []string{"id_token", chatGPTAccountIDExtraKey, OpenAIAPIKeyExtraKey} {
		value := tokenExtraString(token, key)
		if value == "" {
			value = tokenExtraString(previous, key)
		}
		if value != "" {
			extra[key] = value
		}
	}
	return token.WithExtra(extra)
}

func cloneToken(token *oauth2.Token) *oauth2.Token {
	if token == nil {
		return nil
	}

	cloned := *token
	return &cloned
}
