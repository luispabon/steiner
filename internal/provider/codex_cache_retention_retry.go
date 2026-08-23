package provider

import "encoding/json"

// openAIErrorEnvelope is the standard OpenAI Platform error body shape:
// {"error":{"message":...,"type":...,"param":...,"code":...}}.
type openAIErrorEnvelope struct {
	Error struct {
		Param string `json:"param"`
		Code  string `json:"code"`
	} `json:"error"`
}

// isCodexPromptCacheRetentionRejection reports whether err is the known
// upstream Codex/ChatGPT OAuth backend defect where prompt_cache_retention is
// rejected as unsupported. steiner's responsesWire never sends this param
// (see wire_responses.go), so this is a backend-side issue: a subset of
// Codex replicas reject a request the backend itself appears to attach the
// param to. Live as of 2026-08; reported upstream at openai/codex#39392,
// affecting the official Codex app too. Remove this once OpenAI fixes it.
func isCodexPromptCacheRetentionRejection(err error) bool {
	httpErr := asHTTPError(err)
	if httpErr == nil || httpErr.StatusCode != 400 {
		return false
	}
	var envelope openAIErrorEnvelope
	if jsonErr := json.Unmarshal([]byte(httpErr.Body), &envelope); jsonErr != nil {
		return false
	}
	return envelope.Error.Param == "prompt_cache_retention" && envelope.Error.Code == "invalid_parameter"
}
