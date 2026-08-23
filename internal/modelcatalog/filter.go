package modelcatalog

import "strings"

// HeuristicallyExcluded reports whether id resembles a non-chat model.
func HeuristicallyExcluded(id string) bool {
	id = strings.ToLower(id)
	for _, prefix := range []string{
		"text-embedding-",
		"dall-e-",
		"gpt-image-",
		"whisper-",
		"tts-",
		"gpt-4o-mini-tts",
		"text-moderation",
		"omni-moderation",
	} {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	for _, exact := range []string{"babbage-002", "davinci-002"} {
		if id == exact {
			return true
		}
	}
	for _, substring := range []string{"embed", "rerank", "guard"} {
		if strings.Contains(id, substring) {
			return true
		}
	}
	return false
}
