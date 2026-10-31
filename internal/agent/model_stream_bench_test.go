package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

// modelStreamBenchDelta is one small streamed content delta, sized like a real
// token-level provider delta.
const modelStreamBenchDelta = "Hello, wor"

// modelStreamBenchChunks builds a stream shaped like a real assistant reply:
// many small content deltas followed by a final chunk carrying the full text,
// which is what the Codex adapter sends.
func modelStreamBenchChunks(deltas int) []provider.ChatChunk {
	chunks := make([]provider.ChatChunk, 0, deltas+1)
	var full strings.Builder
	full.Grow(deltas * len(modelStreamBenchDelta))
	for range deltas {
		full.WriteString(modelStreamBenchDelta)
		chunks = append(chunks, provider.ChatChunk{Delta: provider.Message{Content: modelStreamBenchDelta}})
	}
	chunks = append(chunks, provider.ChatChunk{
		Done:         true,
		FinishReason: "stop",
		Delta:        provider.Message{Content: full.String()},
	})
	return chunks
}

// BenchmarkConsumeModelStreamSmallChunks measures the consumer cost of
// accumulating 20,000 small deltas into the assistant message. The channel is
// refilled between iterations outside the timed region so the measurement
// covers consumption only.
func BenchmarkConsumeModelStreamSmallChunks(b *testing.B) {
	const deltas = 20000
	chunks := modelStreamBenchChunks(deltas)
	// The final chunk carries the full reply, so it is the expected result.
	want := chunks[len(chunks)-1].Delta.Content
	ctx := context.Background()

	b.ReportAllocs()
	b.SetBytes(int64(len(want)))
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		stream := make(chan provider.ChatChunk, len(chunks))
		for _, chunk := range chunks {
			stream <- chunk
		}
		close(stream)
		b.StartTimer()

		response, err := consumeModelStream(ctx, output.NoopSink{}, 1, stream, output.ChunkSourceAssistant, nil)
		if err != nil {
			b.Fatal(err)
		}
		if response.Message.Content != want {
			b.Fatalf("content = %q, want %q", response.Message.Content, want)
		}
	}
}
