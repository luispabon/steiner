package lsp

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// impl is the concrete implementation of the session interface.
type impl struct {
	conn            jsonrpc2.Conn
	server          protocol.Server
	cmd             *exec.Cmd
	mux             sync.Mutex
	exited          chan struct{}
	diagnosticsChan chan PublishedDiagnostics
	progressChan    chan ProgressEvent
	clientHandler   *clientHandler
}

type clientHandler struct {
	diagnosticsChan chan PublishedDiagnostics
	progressChan    chan ProgressEvent
}

// newTransport creates a new session by spawning a language server process.
func newTransport(ctx context.Context, spec TransportSpec) (session, error) {
	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	cmd.Env = spec.Env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	// Attach stderr to the provided writer
	if spec.Stderr != nil {
		cmd.Stderr = spec.Stderr
	} else {
		cmd.Stderr = io.Discard
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start server: %w", err)
	}

	// Create the jsonrpc2 stream
	stream := jsonrpc2.NewStream(
		&readWriteCloser{
			r: stdout,
			w: stdin,
		},
	)

	// Create the client handler for forwarding notifications
	diagnosticsChan := make(chan PublishedDiagnostics, 10)
	progressChan := make(chan ProgressEvent, 10)
	handler := &clientHandler{
		diagnosticsChan: diagnosticsChan,
		progressChan:    progressChan,
	}

	// Create the protocol client and server
	initCtx, conn, server := protocol.NewClient(ctx, handler, stream)

	// Perform handshake
	if err := handshake(initCtx, conn, server, spec.RootPath); err != nil {
		conn.Close()
		cmd.Process.Kill()
		return nil, fmt.Errorf("handshake: %w", err)
	}

	// Create the session
	exited := make(chan struct{})
	s := &impl{
		conn:            conn,
		server:          server,
		cmd:             cmd,
		exited:          exited,
		diagnosticsChan: diagnosticsChan,
		progressChan:    progressChan,
		clientHandler:   handler,
	}

	// Start goroutine to wait for process exit
	go func() {
		cmd.Wait()
		close(exited)
		close(diagnosticsChan)
		close(progressChan)
	}()

	return s, nil
}

// Definition requests the definition of a symbol at the given position.
func (s *impl) Definition(ctx context.Context, file string, line, col int) ([]Location, error) {
	select {
	case <-s.exited:
		return nil, errServerExited
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	fileURI := uri.File(file)
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
			Position: protocol.Position{
				Line:      uint32(line - 1),
				Character: uint32(col - 1),
			},
		},
	}

	// DefinitionHandler returns []Location, LocationLink, or nil
	result, err := s.callWithExitCheck(ctx, func() (interface{}, error) {
		return s.server.Definition(ctx, &params)
	})
	if err != nil {
		return nil, err
	}

	if result == nil {
		return []Location{}, nil
	}

	// Handle LocationLink which is returned by modern LSP servers
	locs := []Location{}
	if link, ok := result.(protocol.LocationLink); ok {
		locs = append(locs, Location{
			File:      string(link.TargetURI),
			Line:      int(link.TargetRange.Start.Line) + 1,
			Column:    int(link.TargetRange.Start.Character) + 1,
			EndLine:   int(link.TargetRange.End.Line) + 1,
			EndColumn: int(link.TargetRange.End.Character) + 1,
		})
	} else if locLink, ok := result.([]protocol.LocationLink); ok {
		for _, link := range locLink {
			locs = append(locs, Location{
				File:      string(link.TargetURI),
				Line:      int(link.TargetRange.Start.Line) + 1,
				Column:    int(link.TargetRange.Start.Character) + 1,
				EndLine:   int(link.TargetRange.End.Line) + 1,
				EndColumn: int(link.TargetRange.End.Character) + 1,
			})
		}
	} else if loc, ok := result.(protocol.Location); ok {
		locs = append(locs, Location{
			File:      string(loc.URI),
			Line:      int(loc.Range.Start.Line) + 1,
			Column:    int(loc.Range.Start.Character) + 1,
			EndLine:   int(loc.Range.End.Line) + 1,
			EndColumn: int(loc.Range.End.Character) + 1,
		})
	} else if locs2, ok := result.([]protocol.Location); ok {
		for _, loc := range locs2 {
			locs = append(locs, Location{
				File:      string(loc.URI),
				Line:      int(loc.Range.Start.Line) + 1,
				Column:    int(loc.Range.Start.Character) + 1,
				EndLine:   int(loc.Range.End.Line) + 1,
				EndColumn: int(loc.Range.End.Character) + 1,
			})
		}
	}
	return locs, nil
}

// References requests all references to a symbol at the given position.
func (s *impl) References(ctx context.Context, file string, line, col int, includeDecl bool) ([]Location, error) {
	select {
	case <-s.exited:
		return nil, errServerExited
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	fileURI := uri.File(file)
	params := protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
			Position: protocol.Position{
				Line:      uint32(line - 1),
				Character: uint32(col - 1),
			},
		},
		Context: protocol.ReferenceContext{
			IncludeDeclaration: includeDecl,
		},
	}

	result, err := s.callWithExitCheck(ctx, func() (interface{}, error) {
		return s.server.References(ctx, &params)
	})
	if err != nil {
		return nil, err
	}

	if result == nil {
		return []Location{}, nil
	}

	// Convert LSP 0-based to 1-based
	locs := make([]Location, 0)
	if locList, ok := result.([]protocol.Location); ok {
		for _, loc := range locList {
			locs = append(locs, Location{
				File:      string(loc.URI),
				Line:      int(loc.Range.Start.Line) + 1,
				Column:    int(loc.Range.Start.Character) + 1,
				EndLine:   int(loc.Range.End.Line) + 1,
				EndColumn: int(loc.Range.End.Character) + 1,
			})
		}
	}
	return locs, nil
}

// DidOpen notifies the server that a document was opened.
func (s *impl) DidOpen(ctx context.Context, file, languageID, text string, version int32) error {
	select {
	case <-s.exited:
		return errServerExited
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	fileURI := uri.File(file)
	params := protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        fileURI,
			LanguageID: protocol.LanguageKind(languageID),
			Version:    version,
			Text:       text,
		},
	}

	if err := s.notifyWithExitCheck(ctx, func() error {
		return s.server.DidOpen(ctx, &params)
	}); err != nil {
		return err
	}
	return nil
}

// DidClose notifies the server that a document was closed.
func (s *impl) DidClose(ctx context.Context, file string) error {
	select {
	case <-s.exited:
		return errServerExited
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	fileURI := uri.File(file)
	params := protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: fileURI},
	}

	if err := s.notifyWithExitCheck(ctx, func() error {
		return s.server.DidClose(ctx, &params)
	}); err != nil {
		return err
	}
	return nil
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
	return s.exited
}

// Close shuts down the server gracefully.
// It sends shutdown request, awaits response, then sends exit notification.
// If the server does not exit within the timeout, it is forcibly terminated.
func (s *impl) Close(ctx context.Context) error {
	s.mux.Lock()
	defer s.mux.Unlock()

	// Send shutdown request
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.server.Shutdown(shutdownCtx); err != nil {
		s.conn.Close()
		s.forceKill()
		return fmt.Errorf("shutdown: %w", err)
	}

	// Send exit notification
	if err := s.server.Exit(context.Background()); err != nil {
		s.conn.Close()
		s.forceKill()
		return fmt.Errorf("exit notification: %w", err)
	}

	// Wait for process to exit with timeout
	exitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- s.cmd.Wait()
	}()

	select {
	case <-waitDone:
		s.conn.Close()
		return nil
	case <-exitCtx.Done():
		s.conn.Close()
		s.forceKill()
		return fmt.Errorf("server did not exit after shutdown")
	}
}

// forceKill terminates the process using platform-specific methods.
func (s *impl) forceKill() {
	if s.cmd.Process != nil {
		killProcess(s.cmd)
	}
}

// callWithExitCheck makes a call and checks for server exit.
func (s *impl) callWithExitCheck(ctx context.Context, fn func() (interface{}, error)) (interface{}, error) {
	done := make(chan interface{}, 1)
	doneErr := make(chan error, 1)

	go func() {
		result, err := fn()
		if err != nil {
			doneErr <- err
			return
		}
		done <- result
	}()

	select {
	case <-s.exited:
		return nil, errServerExited
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-doneErr:
		return nil, err
	case result := <-done:
		return result, nil
	}
}

// notifyWithExitCheck sends a notification and checks for server exit.
func (s *impl) notifyWithExitCheck(ctx context.Context, fn func() error) error {
	done := make(chan error, 1)

	go func() {
		done <- fn()
	}()

	select {
	case <-s.exited:
		return errServerExited
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

// Implement protocol.Client interface for receiving notifications
func (ch *clientHandler) Progress(ctx context.Context, params *protocol.ProgressParams) error {
	event := ProgressEvent{
		Token: fmt.Sprintf("%v", params.Token),
		Kind:  "progress",
	}

	select {
	case ch.progressChan <- event:
	case <-ctx.Done():
	}
	return nil
}

func (ch *clientHandler) PublishDiagnostics(ctx context.Context, params *protocol.PublishDiagnosticsParams) error {
	var version *int32
	if v, ok := params.Version.Get(); ok {
		version = &v
	}

	diags := PublishedDiagnostics{
		File:    string(params.URI),
		Version: version,
		Items:   make([]Diagnostic, len(params.Diagnostics)),
	}
	for i, d := range params.Diagnostics {
		source := ""
		if s, ok := d.Source.Get(); ok {
			source = s
		}

		message := ""
		if d.Message != nil {
			message = fmt.Sprintf("%v", d.Message)
		}

		diags.Items[i] = Diagnostic{
			File:     string(params.URI),
			Line:     int(d.Range.Start.Line) + 1,
			Column:   int(d.Range.Start.Character) + 1,
			Severity: severityToString(d.Severity),
			Source:   source,
			Message:  message,
			Code:     "",
		}
		if d.Code != nil {
			diags.Items[i].Code = fmt.Sprintf("%v", d.Code)
		}
	}
	select {
	case ch.diagnosticsChan <- diags:
	case <-ctx.Done():
	}
	return nil
}

// severityToString converts a DiagnosticSeverity to a string.
func severityToString(severity protocol.DiagnosticSeverity) string {
	switch severity {
	case protocol.DiagnosticSeverityError:
		return "error"
	case protocol.DiagnosticSeverityWarning:
		return "warning"
	case protocol.DiagnosticSeverityInformation:
		return "information"
	case protocol.DiagnosticSeverityHint:
		return "hint"
	default:
		return fmt.Sprintf("%d", severity)
	}
}

// UnimplementedClient provides no-op implementations for client methods
type UnimplementedClient struct{}

func (uc *UnimplementedClient) Progress(ctx context.Context, params *protocol.ProgressParams) error {
	return nil
}

func (uc *UnimplementedClient) LogTrace(ctx context.Context, params *protocol.LogTraceParams) error {
	return nil
}

func (uc *UnimplementedClient) RegisterCapability(ctx context.Context, params *protocol.RegistrationParams) error {
	return nil
}

func (uc *UnimplementedClient) UnregisterCapability(ctx context.Context, params *protocol.UnregistrationParams) error {
	return nil
}

func (uc *UnimplementedClient) ShowMessage(ctx context.Context, params *protocol.ShowMessageParams) error {
	return nil
}

func (uc *UnimplementedClient) ShowMessageRequest(ctx context.Context, params *protocol.ShowMessageRequestParams) (*protocol.MessageActionItem, error) {
	return nil, nil
}

func (uc *UnimplementedClient) LogMessage(ctx context.Context, params *protocol.LogMessageParams) error {
	return nil
}

func (uc *UnimplementedClient) ShowDocument(ctx context.Context, params *protocol.ShowDocumentParams) (*protocol.ShowDocumentResult, error) {
	return nil, nil
}

func (uc *UnimplementedClient) WorkDoneProgressCreate(ctx context.Context, params *protocol.WorkDoneProgressCreateParams) error {
	return nil
}

func (uc *UnimplementedClient) Telemetry(ctx context.Context, params protocol.LSPAny) error {
	return nil
}

func (uc *UnimplementedClient) PublishDiagnostics(ctx context.Context, params *protocol.PublishDiagnosticsParams) error {
	return nil
}

func (uc *UnimplementedClient) Configuration(ctx context.Context, params *protocol.ConfigurationParams) ([]protocol.LSPAny, error) {
	return nil, nil
}

func (uc *UnimplementedClient) WorkspaceFolders(ctx context.Context) ([]protocol.WorkspaceFolder, error) {
	return nil, nil
}

func (uc *UnimplementedClient) ApplyEdit(ctx context.Context, params *protocol.ApplyWorkspaceEditParams) (*protocol.ApplyWorkspaceEditResult, error) {
	return nil, nil
}

func (uc *UnimplementedClient) CodeLensRefresh(ctx context.Context) error {
	return nil
}

func (uc *UnimplementedClient) FoldingRangeRefresh(ctx context.Context) error {
	return nil
}

func (uc *UnimplementedClient) SemanticTokensRefresh(ctx context.Context) error {
	return nil
}

func (uc *UnimplementedClient) InlineValueRefresh(ctx context.Context) error {
	return nil
}

func (uc *UnimplementedClient) InlayHintRefresh(ctx context.Context) error {
	return nil
}

func (uc *UnimplementedClient) DiagnosticRefresh(ctx context.Context) error {
	return nil
}

func (uc *UnimplementedClient) TextDocumentContentRefresh(ctx context.Context, params *protocol.TextDocumentContentRefreshParams) error {
	return nil
}

// clientHandler embeds UnimplementedClient to satisfy the interface
var _ protocol.Client = (*clientHandler)(nil)

// Ensure clientHandler has all required methods
func (ch *clientHandler) LogTrace(ctx context.Context, params *protocol.LogTraceParams) error {
	return nil
}

func (ch *clientHandler) RegisterCapability(ctx context.Context, params *protocol.RegistrationParams) error {
	return nil
}

func (ch *clientHandler) UnregisterCapability(ctx context.Context, params *protocol.UnregistrationParams) error {
	return nil
}

func (ch *clientHandler) ShowMessage(ctx context.Context, params *protocol.ShowMessageParams) error {
	return nil
}

func (ch *clientHandler) ShowMessageRequest(ctx context.Context, params *protocol.ShowMessageRequestParams) (*protocol.MessageActionItem, error) {
	return nil, nil
}

func (ch *clientHandler) LogMessage(ctx context.Context, params *protocol.LogMessageParams) error {
	return nil
}

func (ch *clientHandler) ShowDocument(ctx context.Context, params *protocol.ShowDocumentParams) (*protocol.ShowDocumentResult, error) {
	return nil, nil
}

func (ch *clientHandler) WorkDoneProgressCreate(ctx context.Context, params *protocol.WorkDoneProgressCreateParams) error {
	return nil
}

func (ch *clientHandler) Telemetry(ctx context.Context, params protocol.LSPAny) error {
	return nil
}

func (ch *clientHandler) Configuration(ctx context.Context, params *protocol.ConfigurationParams) ([]protocol.LSPAny, error) {
	return nil, nil
}

func (ch *clientHandler) WorkspaceFolders(ctx context.Context) ([]protocol.WorkspaceFolder, error) {
	return nil, nil
}

func (ch *clientHandler) ApplyEdit(ctx context.Context, params *protocol.ApplyWorkspaceEditParams) (*protocol.ApplyWorkspaceEditResult, error) {
	return nil, nil
}

func (ch *clientHandler) CodeLensRefresh(ctx context.Context) error {
	return nil
}

func (ch *clientHandler) FoldingRangeRefresh(ctx context.Context) error {
	return nil
}

func (ch *clientHandler) SemanticTokensRefresh(ctx context.Context) error {
	return nil
}

func (ch *clientHandler) InlineValueRefresh(ctx context.Context) error {
	return nil
}

func (ch *clientHandler) InlayHintRefresh(ctx context.Context) error {
	return nil
}

func (ch *clientHandler) DiagnosticRefresh(ctx context.Context) error {
	return nil
}

func (ch *clientHandler) TextDocumentContentRefresh(ctx context.Context, params *protocol.TextDocumentContentRefreshParams) error {
	return nil
}

// readWriteCloser adapts io.Reader and io.Writer to io.ReadWriteCloser.
type readWriteCloser struct {
	r io.Reader
	w io.Writer
}

func (rwc *readWriteCloser) Read(b []byte) (int, error) {
	return rwc.r.Read(b)
}

func (rwc *readWriteCloser) Write(b []byte) (int, error) {
	return rwc.w.Write(b)
}

func (rwc *readWriteCloser) Close() error {
	return nil
}

// TransportSpec specifies parameters for creating a new transport.
type TransportSpec struct {
	Command  string
	Args     []string
	Env      []string
	RootPath string
	Stderr   io.Writer
}
