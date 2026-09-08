package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// session is one live connection to a language server process.
type session interface {
	Definition(ctx context.Context, file string, line, col int) ([]Location, error)
	Implementation(ctx context.Context, file string, line, col int) ([]Location, error)
	TypeDefinition(ctx context.Context, file string, line, col int) ([]Location, error)
	References(ctx context.Context, file string, line, col int, includeDecl bool) ([]Location, error)
	Hover(ctx context.Context, file string, line, col int) (HoverContent, error)
	WorkspaceSymbol(ctx context.Context, query string) ([]SymbolInfo, error)
	DocumentSymbol(ctx context.Context, file string) ([]SymbolInfo, error)
	DidOpen(ctx context.Context, file, languageID, text string, version int32) error
	DidClose(ctx context.Context, file string) error
	// Diagnostics streams publishDiagnostics notifications. The channel is never
	// closed; select on Exited to observe termination.
	Diagnostics() <-chan PublishedDiagnostics
	// Progress streams $/progress notifications. The channel is never closed;
	// select on Exited to observe termination.
	Progress() <-chan ProgressEvent
	Exited() <-chan struct{}
	Close(ctx context.Context) error
}

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

// Definition requests the definition of a symbol at the given position.
func (s *impl) Definition(ctx context.Context, file string, line, col int) ([]Location, error) {
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: textPosition(file, line, col),
	}

	result, err := s.callWithExitCheck(ctx, func() (any, error) {
		return s.server.Definition(ctx, &params)
	})
	if err != nil {
		return nil, err
	}

	locs := []Location{}
	switch r := result.(type) {
	case *protocol.Location:
		if r != nil {
			locs = append(locs, toLocation(r.URI, r.Range))
		}
	case protocol.LocationSlice:
		for i := range r {
			locs = append(locs, toLocation(r[i].URI, r[i].Range))
		}
	case protocol.DefinitionLinkSlice:
		for i := range r {
			locs = append(locs, toLocation(r[i].TargetURI, r[i].TargetRange))
		}
	}
	return locs, nil
}

// Implementation requests the concrete implementations of an interface or
// interface method at the given position. protocol.ImplementationResult is an
// alias of protocol.DefinitionResult, so the three result arms are identical
// to Definition's.
func (s *impl) Implementation(ctx context.Context, file string, line, col int) ([]Location, error) {
	params := protocol.ImplementationParams{
		TextDocumentPositionParams: textPosition(file, line, col),
	}

	result, err := s.callWithExitCheck(ctx, func() (any, error) {
		return s.server.Implementation(ctx, &params)
	})
	if err != nil {
		return nil, err
	}

	locs := []Location{}
	switch r := result.(type) {
	case *protocol.Location:
		if r != nil {
			locs = append(locs, toLocation(r.URI, r.Range))
		}
	case protocol.LocationSlice:
		for i := range r {
			locs = append(locs, toLocation(r[i].URI, r[i].Range))
		}
	case protocol.DefinitionLinkSlice:
		for i := range r {
			locs = append(locs, toLocation(r[i].TargetURI, r[i].TargetRange))
		}
	}
	return locs, nil
}

// TypeDefinition requests the type declaration for the symbol at the given
// position. protocol.TypeDefinitionResult is likewise an alias of
// protocol.DefinitionResult.
func (s *impl) TypeDefinition(ctx context.Context, file string, line, col int) ([]Location, error) {
	params := protocol.TypeDefinitionParams{
		TextDocumentPositionParams: textPosition(file, line, col),
	}

	result, err := s.callWithExitCheck(ctx, func() (any, error) {
		return s.server.TypeDefinition(ctx, &params)
	})
	if err != nil {
		return nil, err
	}

	locs := []Location{}
	switch r := result.(type) {
	case *protocol.Location:
		if r != nil {
			locs = append(locs, toLocation(r.URI, r.Range))
		}
	case protocol.LocationSlice:
		for i := range r {
			locs = append(locs, toLocation(r[i].URI, r[i].Range))
		}
	case protocol.DefinitionLinkSlice:
		for i := range r {
			locs = append(locs, toLocation(r[i].TargetURI, r[i].TargetRange))
		}
	}
	return locs, nil
}

// References requests all references to a symbol at the given position.
func (s *impl) References(ctx context.Context, file string, line, col int, includeDecl bool) ([]Location, error) {
	params := protocol.ReferenceParams{
		TextDocumentPositionParams: textPosition(file, line, col),
		Context:                    protocol.ReferenceContext{IncludeDeclaration: includeDecl},
	}

	result, err := s.callWithExitCheck(ctx, func() (any, error) {
		return s.server.References(ctx, &params)
	})
	if err != nil {
		return nil, err
	}

	locs := []Location{}
	if found, ok := result.([]protocol.Location); ok {
		for i := range found {
			locs = append(locs, toLocation(found[i].URI, found[i].Range))
		}
	}
	return locs, nil
}

// Hover requests hover information at the given position.
func (s *impl) Hover(ctx context.Context, file string, line, col int) (HoverContent, error) {
	params := protocol.HoverParams{
		TextDocumentPositionParams: textPosition(file, line, col),
	}

	result, err := s.callWithExitCheck(ctx, func() (any, error) {
		return s.server.Hover(ctx, &params)
	})
	if err != nil {
		return HoverContent{}, err
	}

	hover, ok := result.(*protocol.Hover)
	if !ok || hover == nil {
		return HoverContent{}, nil
	}

	return HoverContent{Text: hoverContentsToText(hover.Contents)}, nil
}

// WorkspaceSymbol searches for symbols matching query across the workspace.
func (s *impl) WorkspaceSymbol(ctx context.Context, query string) ([]SymbolInfo, error) {
	params := protocol.WorkspaceSymbolParams{Query: query}

	result, err := s.callWithExitCheck(ctx, func() (any, error) {
		return s.server.Symbols(ctx, &params)
	})
	if err != nil {
		return nil, err
	}

	symbols := []SymbolInfo{}
	switch r := result.(type) {
	case protocol.SymbolInformationSlice:
		for i := range r {
			symbols = append(symbols, symbolInformationToSymbolInfo(r[i]))
		}
	case protocol.WorkspaceSymbolSlice:
		for i := range r {
			symbols = append(symbols, workspaceSymbolToSymbolInfo(r[i]))
		}
	}
	return symbols, nil
}

// DocumentSymbol requests the outline of symbols in file.
func (s *impl) DocumentSymbol(ctx context.Context, file string) ([]SymbolInfo, error) {
	params := protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(file)},
	}

	result, err := s.callWithExitCheck(ctx, func() (any, error) {
		return s.server.DocumentSymbol(ctx, &params)
	})
	if err != nil {
		return nil, err
	}

	symbols := []SymbolInfo{}
	switch r := result.(type) {
	case protocol.SymbolInformationSlice:
		for i := range r {
			symbols = append(symbols, symbolInformationToSymbolInfo(r[i]))
		}
	case protocol.DocumentSymbolSlice:
		symbols = append(symbols, flattenDocumentSymbols(file, r, "")...)
	}
	return symbols, nil
}

// symbolInformationToSymbolInfo converts a flat SymbolInformation (shared by
// workspace/symbol and textDocument/documentSymbol's flat arm) into a SymbolInfo.
func symbolInformationToSymbolInfo(si protocol.SymbolInformation) SymbolInfo {
	container := ""
	if si.ContainerName != nil {
		container = *si.ContainerName
	}
	return SymbolInfo{
		Location:  toLocation(si.Location.URI, si.Location.Range),
		Name:      si.Name,
		Kind:      symbolKindName(si.Kind),
		Container: container,
	}
}

// workspaceSymbolToSymbolInfo converts a WorkspaceSymbol, whose Location may be
// a full Location or a LocationUriOnly (no range), into a SymbolInfo.
func workspaceSymbolToSymbolInfo(ws protocol.WorkspaceSymbol) SymbolInfo {
	container := ""
	if ws.ContainerName != nil {
		container = *ws.ContainerName
	}

	var loc Location
	switch l := ws.Location.(type) {
	case *protocol.Location:
		if l != nil {
			loc = toLocation(l.URI, l.Range)
		}
	case *protocol.LocationUriOnly:
		if l != nil {
			loc = Location{File: l.URI.FsPath(), Line: 1, Column: 1}
		}
	}

	return SymbolInfo{
		Location:  loc,
		Name:      ws.Name,
		Kind:      symbolKindName(ws.Kind),
		Container: container,
	}
}

// flattenDocumentSymbols recursively flattens a nested DocumentSymbol tree into
// a flat slice, passing each node's Name down as its children's Container.
func flattenDocumentSymbols(file string, syms []protocol.DocumentSymbol, container string) []SymbolInfo {
	var out []SymbolInfo
	for _, sym := range syms {
		out = append(out, SymbolInfo{
			Location: Location{
				File:      file,
				Line:      int(sym.Range.Start.Line) + 1,
				Column:    int(sym.Range.Start.Character) + 1,
				EndLine:   int(sym.Range.End.Line) + 1,
				EndColumn: int(sym.Range.End.Character) + 1,
			},
			Name:      sym.Name,
			Kind:      symbolKindName(sym.Kind),
			Container: container,
		})
		if len(sym.Children) > 0 {
			out = append(out, flattenDocumentSymbols(file, sym.Children, sym.Name)...)
		}
	}
	return out
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

// Close shuts the server down gracefully: it sends shutdown, awaits the
// response, then sends exit. A server that does not exit within
// shutdownTimeout is force-terminated.
func (s *impl) Close(ctx context.Context) error {
	s.mux.Lock()
	defer s.mux.Unlock()

	shutdownCtx, cancel := context.WithTimeout(ctx, s.shutdownTimeout)
	defer cancel()

	if err := s.server.Shutdown(shutdownCtx); err != nil {
		return s.abort(fmt.Errorf("shutdown: %w", err))
	}
	if err := s.server.Exit(shutdownCtx); err != nil {
		return s.abort(fmt.Errorf("exit notification: %w", err))
	}

	timer := time.NewTimer(s.shutdownTimeout)
	defer timer.Stop()

	select {
	case <-s.proc.Exited():
		s.connCancel()
		_ = s.conn.Close() // best-effort: the peer usually drops the connection first
		return nil
	case <-timer.C:
		return s.abort(errors.New("server did not exit after shutdown"))
	}
}

// abort tears down an unresponsive server and returns the originating cause.
func (s *impl) abort(cause error) error {
	s.connCancel()
	_ = s.conn.Close() // best-effort: the session is being torn down regardless
	s.proc.Kill()
	return cause
}

// callWithExitCheck runs a request, failing fast if the server exits first.
func (s *impl) callWithExitCheck(ctx context.Context, fn func() (any, error)) (any, error) {
	if err := s.exitCheck(); err != nil {
		return nil, err
	}

	type outcome struct {
		result any
		err    error
	}
	done := make(chan outcome, 1)

	go func() {
		result, err := fn()
		done <- outcome{result: result, err: err}
	}()

	select {
	case <-s.proc.Exited():
		return nil, errServerExited
	case <-ctx.Done():
		return nil, ctx.Err()
	case out := <-done:
		return out.result, out.err
	}
}

// exitCheck reports errServerExited once the server is known to be gone, so a
// request issued after the exit never races the transport error into the caller.
func (s *impl) exitCheck() error {
	select {
	case <-s.proc.Exited():
		return errServerExited
	default:
		return nil
	}
}

// notifyWithExitCheck sends a notification, failing fast if the server exits first.
func (s *impl) notifyWithExitCheck(ctx context.Context, fn func() error) error {
	if err := s.exitCheck(); err != nil {
		return err
	}

	done := make(chan error, 1)

	go func() {
		done <- fn()
	}()

	select {
	case <-s.proc.Exited():
		return errServerExited
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

// clientHandler forwards the server-initiated traffic steiner cares about onto
// channels, and answers the remaining client requests with benign defaults.
type clientHandler struct {
	protocol.UnimplementedClient

	diagnostics chan<- PublishedDiagnostics
	progress    chan<- ProgressEvent
}

var _ protocol.Client = (*clientHandler)(nil)

func (ch *clientHandler) Progress(ctx context.Context, params *protocol.ProgressParams) error {
	event, ok := decodeProgress(params)
	if !ok {
		return nil
	}
	select {
	case ch.progress <- event:
	case <-ctx.Done():
	}
	return nil
}

func (ch *clientHandler) PublishDiagnostics(ctx context.Context, params *protocol.PublishDiagnosticsParams) error {
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
	case <-ctx.Done():
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

// hoverContentsToText normalizes hover contents from the LSP protocol into plain text.
// Handles four arms: MarkupContent, String (deprecated), MarkedStringWithLanguage (deprecated),
// and MarkedStringSlice (deprecated). Returns an empty string for nil input.
func hoverContentsToText(contents protocol.HoverContents) string {
	if contents == nil {
		return ""
	}

	switch c := contents.(type) {
	case *protocol.MarkupContent:
		if c != nil {
			return c.Value
		}
	case protocol.String:
		return string(c)
	case *protocol.MarkedStringWithLanguage: //nolint:staticcheck // sent for older servers
		if c != nil {
			return fmt.Sprintf("```%s\n%s\n```", c.Language, c.Value)
		}
	case protocol.MarkedStringSlice:
		var parts []string
		for i := range c {
			text := markedStringToText(c[i])
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}

// markedStringToText normalizes a single MarkedString (which is a union of
// string or MarkedStringWithLanguage) into plain text.
func markedStringToText(ms protocol.MarkedString) string { //nolint:staticcheck // sent for older servers
	if ms == nil {
		return ""
	}

	switch m := ms.(type) {
	case protocol.String:
		return string(m)
	case *protocol.MarkedStringWithLanguage: //nolint:staticcheck // sent for older servers
		if m != nil {
			return fmt.Sprintf("```%s\n%s\n```", m.Language, m.Value)
		}
	}
	return ""
}

// messageText renders a diagnostic message, which is either a plain string or
// markup content.
func messageText(message protocol.InlayHintTooltip) string {
	switch m := message.(type) {
	case protocol.String:
		return string(m)
	case *protocol.MarkupContent:
		if m != nil {
			return m.Value
		}
	}
	return ""
}

// tokenString renders a string-or-integer union (progress tokens, diagnostic codes).
func tokenString(token protocol.ProgressToken) string {
	switch t := token.(type) {
	case protocol.String:
		return string(t)
	case protocol.Integer:
		return fmt.Sprintf("%d", int32(t))
	}
	return ""
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

// symbolKindNames maps SymbolKind to a human-readable string.
var symbolKindNames = map[protocol.SymbolKind]string{
	protocol.SymbolKindFile:          "file",
	protocol.SymbolKindModule:        "module",
	protocol.SymbolKindNamespace:     "namespace",
	protocol.SymbolKindPackage:       "package",
	protocol.SymbolKindClass:         "class",
	protocol.SymbolKindMethod:        "method",
	protocol.SymbolKindProperty:      "property",
	protocol.SymbolKindField:         "field",
	protocol.SymbolKindConstructor:   "constructor",
	protocol.SymbolKindEnum:          "enum",
	protocol.SymbolKindInterface:     "interface",
	protocol.SymbolKindFunction:      "function",
	protocol.SymbolKindVariable:      "var",
	protocol.SymbolKindConstant:      "const",
	protocol.SymbolKindString:        "string",
	protocol.SymbolKindNumber:        "number",
	protocol.SymbolKindBoolean:       "boolean",
	protocol.SymbolKindArray:         "array",
	protocol.SymbolKindObject:        "object",
	protocol.SymbolKindKey:           "key",
	protocol.SymbolKindNull:          "null",
	protocol.SymbolKindEnumMember:    "enum_member",
	protocol.SymbolKindStruct:        "struct",
	protocol.SymbolKindEvent:         "event",
	protocol.SymbolKindOperator:      "operator",
	protocol.SymbolKindTypeParameter: "type_parameter",
}

// symbolKindName converts a SymbolKind to a human-readable string, falling
// back to the numeric value for kinds not enumerated here.
func symbolKindName(k protocol.SymbolKind) string {
	if name, ok := symbolKindNames[k]; ok {
		return name
	}
	return fmt.Sprintf("%d", k)
}
