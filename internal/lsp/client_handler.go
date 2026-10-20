package lsp

import (
	"context"
	"encoding/json"

	"go.lsp.dev/protocol"
)

// clientHandler forwards the server-initiated traffic steiner cares about onto
// channels, and answers the remaining client requests with benign defaults.
type clientHandler struct {
	protocol.UnimplementedClient

	// diagnostics is bidirectional so a full channel can evict its oldest
	// publication in favour of the newest one.
	diagnostics chan PublishedDiagnostics
	progress    chan ProgressEvent
}

var _ protocol.Client = (*clientHandler)(nil)

func (ch *clientHandler) Progress(_ context.Context, params *protocol.ProgressParams) error {
	event, ok := decodeProgress(params)
	if !ok {
		return nil
	}
	select {
	case ch.progress <- event:
	default:
		// Backpressure: the consumer is behind, so the newest event is dropped
		// rather than parking this notification handler on a full channel.
	}
	return nil
}

func (ch *clientHandler) PublishDiagnostics(_ context.Context, params *protocol.PublishDiagnosticsParams) error {
	file := params.URI.FsPath()

	published := PublishedDiagnostics{
		File:  file,
		Items: make([]Diagnostic, len(params.Diagnostics)),
	}
	if v, ok := params.Version.Get(); ok {
		published.Version = &v
	}
	for i, d := range params.Diagnostics {
		source, _ := d.Source.Get()
		published.Items[i] = Diagnostic{
			File:     file,
			Line:     int(d.Range.Start.Line) + 1,
			Column:   int(d.Range.Start.Character) + 1,
			Severity: severityToString(d.Severity),
			Source:   source,
			Message:  messageText(d.Message),
			Code:     tokenString(d.Code),
		}
	}

	select {
	case ch.diagnostics <- published:
	default:
		// Backpressure: evict the oldest queued publication so the newest state
		// survives. Consuming a stale publication later would report diagnostics
		// the server has already replaced.
		select {
		case <-ch.diagnostics:
		default:
		}
		select {
		case ch.diagnostics <- published:
		default:
		}
	}
	return nil
}

// RegisterCapability acknowledges dynamic registration; steiner issues every
// request unconditionally, so there is no client-side registry to update.
func (ch *clientHandler) RegisterCapability(context.Context, *protocol.RegistrationParams) error {
	return nil
}

// UnregisterCapability mirrors RegisterCapability.
func (ch *clientHandler) UnregisterCapability(context.Context, *protocol.UnregistrationParams) error {
	return nil
}

// WorkDoneProgressCreate accepts the token; progress is reported through Progress.
func (ch *clientHandler) WorkDoneProgressCreate(context.Context, *protocol.WorkDoneProgressCreateParams) error {
	return nil
}

// Configuration answers with a null per requested item: steiner supplies server
// settings through InitializationOptions instead.
func (ch *clientHandler) Configuration(_ context.Context, params *protocol.ConfigurationParams) ([]protocol.LSPAny, error) {
	settings := make([]protocol.LSPAny, len(params.Items))
	for i := range settings {
		settings[i] = protocol.LSPAny("null")
	}
	return settings, nil
}

// WorkspaceFolders reports no folders; the root is supplied at initialize time.
func (ch *clientHandler) WorkspaceFolders(context.Context) ([]protocol.WorkspaceFolder, error) {
	return nil, nil
}

// The refresh requests below are acknowledged and dropped: steiner queries the
// server on demand and holds no cached results to invalidate.
func (ch *clientHandler) CodeLensRefresh(context.Context) error       { return nil }
func (ch *clientHandler) FoldingRangeRefresh(context.Context) error   { return nil }
func (ch *clientHandler) SemanticTokensRefresh(context.Context) error { return nil }
func (ch *clientHandler) InlineValueRefresh(context.Context) error    { return nil }
func (ch *clientHandler) InlayHintRefresh(context.Context) error      { return nil }
func (ch *clientHandler) DiagnosticRefresh(context.Context) error     { return nil }

// progressValue is the shared shape of the three $/progress payload variants.
type progressValue struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Message string `json:"message"`
}

// decodeProgress converts a $/progress notification into a ProgressEvent.
// It reports false for payloads that are not one of the three work-done
// variants, so callers never observe a Kind outside begin/report/end.
func decodeProgress(params *protocol.ProgressParams) (ProgressEvent, bool) {
	var value progressValue
	if err := json.Unmarshal(params.Value, &value); err != nil {
		return ProgressEvent{}, false
	}
	switch value.Kind {
	case "begin", "report", "end":
	default:
		return ProgressEvent{}, false
	}

	message := value.Message
	// begin carries a mandatory title and an optional message; fall back to the
	// title so the event is never empty.
	if message == "" && value.Kind == "begin" {
		message = value.Title
	}

	return ProgressEvent{
		Token:   tokenString(params.Token),
		Kind:    value.Kind,
		Message: message,
	}, true
}
