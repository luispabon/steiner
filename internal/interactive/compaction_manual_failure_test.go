package interactive

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// agentCompactRunner runs the real agent compaction behind the runner seam, so
// the agent reports its own failure. Manual compaction never calls Run, so the
// embedded runExecutor stays nil.
type agentCompactRunner struct {
	runExecutor
	req agent.RunRequest
}

func (r agentCompactRunner) CompactWithLineage(ctx context.Context, conversation []agent.Message, lineage agent.ConversationLineage, _ agent.LastMainRequest, _ []provider.ToolSpec, steering string) ([]agent.Message, bool, bool, error) {
	return agent.NewRunner().CompactWithLineage(ctx, r.req, lineage, conversation, steering)
}

// emptyReplyCompactionProvider answers every call with no summary text.
type emptyReplyCompactionProvider struct{ calls int }

func (p *emptyReplyCompactionProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	p.calls++
	return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant}, FinishReason: "stop"}, nil
}

func (p *emptyReplyCompactionProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, errors.New("stream not used")
}

func (p *emptyReplyCompactionProvider) SupportsUsageStats() bool { return true }

// TestManualCompactionEmptySummaryReportsOnce proves a manual compaction whose
// summary stays empty shows the compaction error, leaves the conversation and
// lineage unchanged, and emits one skipped failure event. The manual path must
// not add its own "no stage applied" event on top.
func TestManualCompactionEmptySummaryReportsOnce(t *testing.T) {
	conv := twoTurnConversation()
	lineage := agent.ConversationLineage{
		Generations:      []agent.ConversationGeneration{{ID: 1, Messages: conv}},
		NextGenerationID: 2,
	}
	prov := &emptyReplyCompactionProvider{}
	var events []output.Event
	sink := output.SinkFunc(func(event output.Event) { events = append(events, event) })
	runner := agentCompactRunner{req: agent.RunRequest{
		Provider:      prov,
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "test", BackendModelID: "test-model"},
		ModelBudget:   prompt.ModelTokenBudget{ContextSize: 100000, MaxCompletionTokens: 256, SummaryMaxTokens: 128},
		Events:        sink,
	}}
	s := mustCompactionSession(t, Dependencies{Runner: runner, BaseEvents: sink})
	drv := agent.NewConversationDriver(agent.DriverOptions{}, conv, lineage)

	gotConv, gotLineage, err := s.manualCompaction(drv, "")(context.Background(), conv)
	if err != nil {
		t.Fatalf("manualCompaction() error = %v, want nil because the run reports it as an event", err)
	}
	if !reflect.DeepEqual(gotConv, conv) || !reflect.DeepEqual(gotLineage, lineage) {
		t.Error("conversation or lineage changed after a failed compaction, want both unchanged")
	}
	if prov.calls != 3 {
		t.Errorf("summary calls = %d, want 3 main-model attempts", prov.calls)
	}

	var stopReasons, skipped, duplicateNotes int
	for _, ev := range events {
		if stop, ok := ev.Payload.(output.StopReasonEvent); ok && strings.HasPrefix(stop.Reason, "Compaction error: ") && strings.Contains(stop.Reason, "compaction summary is empty") {
			stopReasons++
		}
		payload, ok := output.AsContextCompactionEvent(ev.Payload)
		if !ok {
			continue
		}
		if payload.Severity == "skipped" {
			skipped++
		}
		for _, note := range output.ContextDiagnosticNotes(ev.Payload) {
			if note == "compaction skipped: no stage applied" {
				duplicateNotes++
			}
		}
	}
	if stopReasons != 1 {
		t.Errorf("compaction error stop reasons = %d, want 1 shown to the user", stopReasons)
	}
	if skipped != 1 {
		t.Errorf("skipped compaction events = %d, want exactly one failure event", skipped)
	}
	if duplicateNotes != 0 {
		t.Errorf("\"no stage applied\" notes = %d, want none on top of the failure event", duplicateNotes)
	}
}
