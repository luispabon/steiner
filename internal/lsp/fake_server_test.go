package lsp

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

// testTimeout bounds every wait in the session tests.
const testTimeout = 2 * time.Second

// fakeProcess is a childProcess double: the test decides when the "process"
// exits and can observe whether it was force-terminated.
type fakeProcess struct {
	exited     chan struct{}
	exitOnce   sync.Once
	killed     chan struct{}
	killedOnce sync.Once
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{exited: make(chan struct{}), killed: make(chan struct{})}
}

func (p *fakeProcess) Exited() <-chan struct{} { return p.exited }

func (p *fakeProcess) Kill() {
	p.killedOnce.Do(func() { close(p.killed) })
	p.markExited()
}

func (p *fakeProcess) markExited() {
	p.exitOnce.Do(func() { close(p.exited) })
}

func (p *fakeProcess) wasKilled() bool {
	select {
	case <-p.killed:
		return true
	default:
		return false
	}
}

// fakeServer is an in-process language server. It records the methods it
// received and lets tests stall or divert individual requests.
type fakeServer struct {
	protocol.UnimplementedServer

	// initializeHold, when non-nil, blocks the initialize response until
	// releaseHolds is called or the request context ends.
	initializeHold chan struct{}
	// definitionHold, when non-nil, blocks the definition response likewise.
	definitionHold chan struct{}
	releaseOnce    sync.Once
	// definitionResult is returned by textDocument/definition.
	definitionResult protocol.DefinitionResult
	// implementationResult is returned by textDocument/implementation.
	// protocol.ImplementationResult is an alias of protocol.DefinitionResult.
	implementationResult protocol.DefinitionResult
	implementationErr    error
	// typeDefinitionResult is returned by textDocument/typeDefinition.
	typeDefinitionResult protocol.DefinitionResult
	typeDefinitionErr    error
	// referencesResult is returned by textDocument/references.
	referencesResult []protocol.Location
	// hoverResult is returned by textDocument/hover.
	hoverResult *protocol.Hover
	// workspaceSymbolResult is returned by workspace/symbol.
	workspaceSymbolResult protocol.WorkspaceSymbolResult
	// documentSymbolResult is returned by textDocument/documentSymbol.
	documentSymbolResult protocol.DocumentSymbolResult
	// ignoreExit makes the server accept exit without ever going away.
	ignoreExit bool

	mu         sync.Mutex
	methods    []string
	initParams *protocol.InitializeParams

	client   protocol.Client
	exitOnce sync.Once
	exited   chan struct{}
	onExit   func()
	// onDidOpen, when non-nil, is invoked after DidOpen is recorded.
	onDidOpen func(context.Context, *protocol.DidOpenTextDocumentParams)
	// onDidClose, when non-nil, is invoked after DidClose is recorded.
	onDidClose func(context.Context, *protocol.DidCloseTextDocumentParams)
}

func newFakeServer() *fakeServer {
	return &fakeServer{exited: make(chan struct{})}
}

// stallInitialize makes the server accept initialize but withhold its response.
func (f *fakeServer) stallInitialize() {
	f.initializeHold = make(chan struct{})
}

// stallDefinition makes the server accept definition but withhold its response.
func (f *fakeServer) stallDefinition() {
	f.definitionHold = make(chan struct{})
}

// releaseHolds lets every stalled handler finish, so the connection can be torn
// down without waiting on an in-flight request.
func (f *fakeServer) releaseHolds() {
	f.releaseOnce.Do(func() {
		if f.initializeHold != nil {
			close(f.initializeHold)
		}
		if f.definitionHold != nil {
			close(f.definitionHold)
		}
	})
}

func (f *fakeServer) record(method string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.methods = append(f.methods, method)
}

func (f *fakeServer) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

func (f *fakeServer) initializeParams() *protocol.InitializeParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.initParams
}

func (f *fakeServer) Initialize(ctx context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	f.record("initialize")
	f.mu.Lock()
	f.initParams = params
	f.mu.Unlock()

	if f.initializeHold != nil {
		select {
		case <-f.initializeHold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			DefinitionProvider:     protocol.Boolean(true),
			ImplementationProvider: protocol.Boolean(true),
			TypeDefinitionProvider: protocol.Boolean(true),
		},
	}, nil
}

func (f *fakeServer) Initialized(context.Context, *protocol.InitializedParams) error {
	f.record("initialized")
	return nil
}

func (f *fakeServer) Definition(ctx context.Context, _ *protocol.DefinitionParams) (protocol.DefinitionResult, error) {
	f.record("textDocument/definition")
	if f.definitionHold != nil {
		select {
		case <-f.definitionHold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.definitionResult, nil
}

func (f *fakeServer) Implementation(_ context.Context, _ *protocol.ImplementationParams) (protocol.ImplementationResult, error) {
	f.record("textDocument/implementation")
	if f.implementationErr != nil {
		return nil, f.implementationErr
	}
	return f.implementationResult, nil
}

func (f *fakeServer) TypeDefinition(_ context.Context, _ *protocol.TypeDefinitionParams) (protocol.TypeDefinitionResult, error) {
	f.record("textDocument/typeDefinition")
	if f.typeDefinitionErr != nil {
		return nil, f.typeDefinitionErr
	}
	return f.typeDefinitionResult, nil
}

func (f *fakeServer) References(context.Context, *protocol.ReferenceParams) ([]protocol.Location, error) {
	f.record("textDocument/references")
	return f.referencesResult, nil
}

func (f *fakeServer) Hover(context.Context, *protocol.HoverParams) (*protocol.Hover, error) {
	f.record("textDocument/hover")
	return f.hoverResult, nil
}

func (f *fakeServer) Symbols(context.Context, *protocol.WorkspaceSymbolParams) (protocol.WorkspaceSymbolResult, error) {
	f.record("workspace/symbol")
	return f.workspaceSymbolResult, nil
}

func (f *fakeServer) DocumentSymbol(context.Context, *protocol.DocumentSymbolParams) (protocol.DocumentSymbolResult, error) {
	f.record("textDocument/documentSymbol")
	return f.documentSymbolResult, nil
}

func (f *fakeServer) DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	f.record("textDocument/didOpen")
	if f.onDidOpen != nil {
		f.onDidOpen(ctx, params)
	}
	return nil
}

func (f *fakeServer) DidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	f.record("textDocument/didClose")
	if f.onDidClose != nil {
		f.onDidClose(ctx, params)
	}
	return nil
}

func (f *fakeServer) Shutdown(context.Context) error {
	f.record("shutdown")
	return nil
}

func (f *fakeServer) Exit(context.Context) error {
	f.record("exit")
	f.exitOnce.Do(func() { close(f.exited) })
	if !f.ignoreExit && f.onExit != nil {
		f.onExit()
	}
	return nil
}

// notifyDiagnostics publishes diagnostics to the connected session.
func (f *fakeServer) notifyDiagnostics(ctx context.Context, t *testing.T, params *protocol.PublishDiagnosticsParams) {
	t.Helper()
	if err := f.client.PublishDiagnostics(ctx, params); err != nil {
		t.Fatalf("publish diagnostics: %v", err)
	}
}

// notifyProgress sends a $/progress notification to the connected session.
func (f *fakeServer) notifyProgress(ctx context.Context, t *testing.T, params *protocol.ProgressParams) {
	t.Helper()
	if err := f.client.Progress(ctx, params); err != nil {
		t.Fatalf("send progress: %v", err)
	}
}

// startFakeSession connects a session to fs over an in-memory pipe and returns
// the handshake outcome. The session is closed at the end of the test.
func startFakeSession(ctx context.Context, t *testing.T, fs *fakeServer, initOpts map[string]any) (*impl, *fakeProcess, error) {
	t.Helper()

	clientPipe, serverPipe := net.Pipe()
	_, serverConn, client := protocol.NewServer(context.Background(), fs, jsonrpc2.NewStream(serverPipe))
	fs.client = client

	proc := newFakeProcess()
	fs.onExit = func() {
		// A real server drops the connection and the process reaps shortly after
		// exit; do the close off the handler goroutine to avoid deadlocking it.
		proc.markExited()
		go func() {
			_ = serverConn.Close()
		}()
	}

	// Cleanups run last-in-first-out: release stalled handlers before either
	// connection is closed, otherwise the close blocks on an in-flight request.
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientPipe.Close()
	})
	t.Cleanup(fs.releaseHolds)

	s, err := newSession(ctx, jsonrpc2.NewStream(clientPipe), t.TempDir(), initOpts, proc)
	if err != nil {
		return nil, proc, err
	}
	s.shutdownTimeout = 200 * time.Millisecond
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		_ = s.Close(closeCtx)
	})
	return s, proc, nil
}
