package lsp

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// errServerExited is returned when the server exits mid-request.
var errServerExited = errors.New("language server exited")

// defaultShutdownTimeout bounds each phase of the graceful shutdown sequence.
const defaultShutdownTimeout = 5 * time.Second

// notifyBuffer is the buffer depth of the diagnostics and progress channels.
const notifyBuffer = 32

// impl is the concrete implementation of the session interface.
type impl struct {
	conn       jsonrpc2.Conn
	connCancel context.CancelFunc
	server     protocol.Server
	proc       childProcess
	// capabilities is retained from the initialize response for later use;
	// request decoding deliberately never branches on it.
	capabilities    protocol.ServerCapabilities
	shutdownTimeout time.Duration
	mux             sync.Mutex
	diagnosticsChan chan PublishedDiagnostics
	progressChan    chan ProgressEvent
}

// newSession performs the LSP handshake over stream and returns the live
// session. Process lifecycle is owned by proc, so callers can supply either a
// spawned child process or a test double.
func newSession(ctx context.Context, stream jsonrpc2.Stream, rootPath string, initOpts map[string]any, proc childProcess) (*impl, error) {
	diagnosticsChan := make(chan PublishedDiagnostics, notifyBuffer)
	progressChan := make(chan ProgressEvent, notifyBuffer)
	handler := &clientHandler{diagnostics: diagnosticsChan, progress: progressChan}

	// The connection must outlive ctx, which only bounds the handshake; it ends
	// with connCancel, which also unblocks any handler waiting on a channel.
	connCtx, connCancel := context.WithCancel(context.WithoutCancel(ctx))
	_, conn, server := protocol.NewClient(connCtx, handler, stream)

	result, err := handshake(ctx, server, rootPath, initOpts)
	if err != nil {
		connCancel()
		_ = conn.Close() // best-effort: the session is being abandoned
		return nil, err
	}

	return &impl{
		conn:            conn,
		connCancel:      connCancel,
		server:          server,
		proc:            proc,
		capabilities:    result.Capabilities,
		shutdownTimeout: defaultShutdownTimeout,
		diagnosticsChan: diagnosticsChan,
		progressChan:    progressChan,
	}, nil
}

// DidOpen notifies the server that a document was opened.
func (s *impl) DidOpen(ctx context.Context, file, languageID, text string, version int32) error {
	params := protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        uri.File(file),
			LanguageID: protocol.LanguageKind(languageID),
			Version:    version,
			Text:       text,
		},
	}

	return s.notifyWithExitCheck(ctx, func() error {
		return s.server.DidOpen(ctx, &params)
	})
}

// DidClose notifies the server that a document was closed.
func (s *impl) DidClose(ctx context.Context, file string) error {
	params := protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(file)},
	}

	return s.notifyWithExitCheck(ctx, func() error {
		return s.server.DidClose(ctx, &params)
	})
}

// Diagnostics returns the channel for receiving published diagnostics.
func (s *impl) Diagnostics() <-chan PublishedDiagnostics {
	return s.diagnosticsChan
}

// Progress returns the channel for receiving progress events.
func (s *impl) Progress() <-chan ProgressEvent {
	return s.progressChan
}

// Exited returns a channel that closes when the server process exits.
func (s *impl) Exited() <-chan struct{} {
	return s.proc.Exited()
}

// textPosition builds the LSP request position, converting the 1-based line and
// column of the steiner API to the 0-based positions of the protocol.
func textPosition(file string, line, col int) protocol.TextDocumentPositionParams {
	return protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(file)},
		Position: protocol.Position{
			Line:      uint32(line - 1),
			Character: uint32(col - 1),
		},
	}
}

// toLocation converts a 0-based protocol range into a 1-based Location.
func toLocation(u uri.URI, r protocol.Range) Location {
	return Location{
		File:      u.FsPath(),
		Line:      int(r.Start.Line) + 1,
		Column:    int(r.Start.Character) + 1,
		EndLine:   int(r.End.Line) + 1,
		EndColumn: int(r.End.Character) + 1,
	}
}
