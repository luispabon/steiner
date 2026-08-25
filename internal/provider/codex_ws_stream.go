package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/coder/websocket"
)

// wsEmitter forwards stream chunks to the caller and records whether anything
// has actually reached it. The unary path passes no sink: it buffers the whole
// response, so nothing is visible until the call returns and a mid-stream
// reconnect stays safe there.
type wsEmitter struct {
	emit    func(ChatChunk) error
	emitted bool
}

func (e *wsEmitter) send(chunk ChatChunk) error {
	if e.emit == nil {
		return nil
	}
	// Recorded before the call: a chunk can reach the consumer even when emit
	// then reports the consumer went away.
	e.emitted = true
	return e.emit(chunk)
}

func buildWSRequestFrame(request ChatRequest, model string) ([]byte, error) {
	wire, err := responsesRequestWire(request, model, false)
	if err != nil {
		return nil, err
	}
	wire.PromptCacheKey = request.PromptCacheKey

	frame := responsesRequestMap(wire)
	if _, ok := frame["type"]; !ok {
		frame["type"] = "response.create"
	}
	delete(frame, "model")
	frame["model"] = model

	return json.Marshal(frame)
}

// sendRequest writes one request frame and drains the response stream, handing
// every chunk to emitter as it is parsed so the caller sees deltas live. The
// returned ChatResponse is the accumulated terminal result, mirroring the
// HTTP/SSE path where incremental deltas are followed by a final chunk carrying
// the whole message.
func (p *codexWSProvider) sendRequest(ctx context.Context, request ChatRequest, emitter *wsEmitter) (ChatResponse, error) {
	payload, err := buildWSRequestFrame(request, p.model)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("build frame: %w", err)
	}

	writeCtx, cancelWrite := context.WithTimeout(ctx, p.writeTimeout)
	err = p.conn.Write(writeCtx, websocket.MessageText, payload)
	cancelWrite()
	if err != nil {
		return ChatResponse{}, fmt.Errorf("write frame: %w", err)
	}

	state := responsesStreamState{}

	for {
		// The deadline is rearmed per frame, so it measures the gap since the
		// last frame rather than the length of the response.
		readCtx, cancelRead := context.WithTimeout(ctx, p.interFrameTimeout)
		typ, data, err := p.conn.Read(readCtx)
		cancelRead()
		if err != nil {
			return ChatResponse{}, fmt.Errorf("read response: %w", err)
		}

		if typ != websocket.MessageText {
			continue
		}

		done, err := processResponsesStreamEvent(&state, string(data), emitter.send)
		if err != nil {
			return ChatResponse{}, fmt.Errorf("process event: %w", err)
		}

		if done {
			break
		}
	}

	if !state.sawDone && state.finishReason == "" {
		return ChatResponse{}, fmt.Errorf("stream completed without a final chunk")
	}

	final := responsesStreamStateToChatChunk(state)
	return ChatResponse{
		Message:      final.Delta,
		Usage:        final.Usage,
		FinishReason: final.FinishReason,
	}, nil
}
