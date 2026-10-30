package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

// fallbackGoldenPath is the fallback-disabled main request fixture. It was
// generated from origin/main 37ed6c42 by TestDumpFallbackGoldenFromMain in a
// scratch copy of that tree (.steiner/tmp/golden-main/internal/agent, not
// committed). That generator uses the same conversation, budgets, system prompt,
// tools, params and extra params as newFallbackOperationRun.
const fallbackGoldenPath = "testdata/fallback_main_golden.json"

// fallbackGoldenRequest is the part of a provider.ChatRequest that the fixture
// records. Field order sets the key order of the fixture.
type fallbackGoldenRequest struct {
	TransportSession         string
	ParentTransportSession   string
	Model                    string
	Messages                 []fallbackGoldenMessage
	MaxTokens                *int
	Stream                   bool
	Tools                    []provider.ToolSpec
	PromptCacheKey           string
	Params                   map[string]any
	ExtraParams              map[string]any
	IncludeEmptyReasoning    bool
	Reasoning                *provider.ReasoningRequest
	AdvisorCacheProfile      bool
	BackendState             string
	CompactionSummary        bool
	CompactionSourceMessages int
	// RawPromptTokenEstimate is estimator metadata, never wire payload. It is
	// recorded only so TestFallbackGoldenRequestCoversChatRequest accepts it, and
	// json:"-" keeps it out of the golden encoding.
	RawPromptTokenEstimate int `json:"-"`
}

// fallbackGoldenMessage is one message reduced to its role and the SHA-256 of
// its JSON encoding. System messages are hashed like the others, so the fixture
// pins the rendered system preamble.
type fallbackGoldenMessage struct {
	Role   string
	SHA256 string
}

// fallbackGoldenBytes returns the fixture encoding of reqs: indented JSON with a
// trailing newline.
func fallbackGoldenBytes(reqs []provider.ChatRequest) ([]byte, error) {
	out := make([]fallbackGoldenRequest, 0, len(reqs))
	for _, r := range reqs {
		msgs, err := fallbackGoldenMessages(r.Messages)
		if err != nil {
			return nil, err
		}
		out = append(out, fallbackGoldenRequest{
			TransportSession:         r.TransportSession,
			ParentTransportSession:   r.ParentTransportSession,
			Model:                    r.Model,
			Messages:                 msgs,
			MaxTokens:                r.MaxTokens,
			Stream:                   r.Stream,
			Tools:                    r.Tools,
			PromptCacheKey:           r.PromptCacheKey,
			Params:                   r.Params,
			ExtraParams:              r.ExtraParams,
			IncludeEmptyReasoning:    r.IncludeEmptyReasoning,
			Reasoning:                r.Reasoning,
			AdvisorCacheProfile:      r.AdvisorCacheProfile,
			BackendState:             string(r.BackendState),
			CompactionSummary:        r.CompactionSummary,
			CompactionSourceMessages: r.CompactionSourceMessages,
		})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal fallback golden: %w", err)
	}
	return append(data, '\n'), nil
}

// fallbackGoldenMessages reduces msgs to fallbackGoldenMessage values.
func fallbackGoldenMessages(msgs []provider.Message) ([]fallbackGoldenMessage, error) {
	out := make([]fallbackGoldenMessage, 0, len(msgs))
	for _, m := range msgs {
		data, err := json.Marshal(m)
		if err != nil {
			return nil, fmt.Errorf("marshal fallback golden message: %w", err)
		}
		sum := sha256.Sum256(data)
		out = append(out, fallbackGoldenMessage{Role: string(m.Role), SHA256: hex.EncodeToString(sum[:])})
	}
	return out, nil
}

// TestFallbackGoldenRequestCoversChatRequest fails when provider.ChatRequest gains
// a field that fallbackGoldenRequest does not record. Several ChatRequest fields
// are excluded from JSON, so the check compares field names, not encoded output.
func TestFallbackGoldenRequestCoversChatRequest(t *testing.T) {
	recorded := reflect.TypeOf(fallbackGoldenRequest{})
	chat := reflect.TypeOf(provider.ChatRequest{})
	for i := range chat.NumField() {
		name := chat.Field(i).Name
		if _, ok := recorded.FieldByName(name); !ok {
			t.Errorf("provider.ChatRequest.%s is not recorded by fallbackGoldenRequest; add it to the fixture", name)
		}
	}
}
