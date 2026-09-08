package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// targetRange is the 0-based range every definition fixture reports.
func targetRange() protocol.Range {
	return protocol.Range{
		Start: protocol.Position{Line: 4, Character: 2},
		End:   protocol.Position{Line: 4, Character: 9},
	}
}

func TestSessionDefinitionRoundTrip(t *testing.T) {
	target := uri.File("/src/target.go")

	tests := []struct {
		name   string
		result protocol.DefinitionResult
		want   []Location
	}{
		{
			name:   "single location",
			result: &protocol.Location{URI: target, Range: targetRange()},
			want:   []Location{{File: "/src/target.go", Line: 5, Column: 3, EndLine: 5, EndColumn: 10}},
		},
		{
			name:   "location slice",
			result: protocol.LocationSlice{{URI: target, Range: targetRange()}},
			want:   []Location{{File: "/src/target.go", Line: 5, Column: 3, EndLine: 5, EndColumn: 10}},
		},
		{
			name:   "definition links",
			result: protocol.DefinitionLinkSlice{{TargetURI: target, TargetRange: targetRange(), TargetSelectionRange: targetRange()}},
			want:   []Location{{File: "/src/target.go", Line: 5, Column: 3, EndLine: 5, EndColumn: 10}},
		},
		{
			name:   "no result",
			result: nil,
			want:   []Location{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			fs := newFakeServer()
			fs.definitionResult = tt.result
			s, _, err := startFakeSession(ctx, t, fs, nil)
			if err != nil {
				t.Fatalf("start session: %v", err)
			}

			got, err := s.Definition(ctx, "/src/caller.go", 12, 4)
			if err != nil {
				t.Fatalf("definition: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("definition = %+v, want %+v", got, tt.want)
			}
			if methods := fs.recorded(); !slices.Contains(methods, "textDocument/definition") {
				t.Errorf("server did not receive definition request, got %v", methods)
			}
		})
	}
}

func TestSessionImplementationRoundTrip(t *testing.T) {
	target := uri.File("/src/target.go")

	tests := []struct {
		name   string
		result protocol.ImplementationResult
		want   []Location
	}{
		{
			name:   "single location",
			result: &protocol.Location{URI: target, Range: targetRange()},
			want:   []Location{{File: "/src/target.go", Line: 5, Column: 3, EndLine: 5, EndColumn: 10}},
		},
		{
			name:   "location slice",
			result: protocol.LocationSlice{{URI: target, Range: targetRange()}},
			want:   []Location{{File: "/src/target.go", Line: 5, Column: 3, EndLine: 5, EndColumn: 10}},
		},
		{
			name:   "definition links",
			result: protocol.DefinitionLinkSlice{{TargetURI: target, TargetRange: targetRange(), TargetSelectionRange: targetRange()}},
			want:   []Location{{File: "/src/target.go", Line: 5, Column: 3, EndLine: 5, EndColumn: 10}},
		},
		{
			name:   "no result",
			result: nil,
			want:   []Location{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			fs := newFakeServer()
			fs.implementationResult = tt.result
			s, _, err := startFakeSession(ctx, t, fs, nil)
			if err != nil {
				t.Fatalf("start session: %v", err)
			}

			got, err := s.Implementation(ctx, "/src/caller.go", 12, 4)
			if err != nil {
				t.Fatalf("implementation: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("implementation = %+v, want %+v", got, tt.want)
			}
			if methods := fs.recorded(); !slices.Contains(methods, "textDocument/implementation") {
				t.Errorf("server did not receive implementation request, got %v", methods)
			}
		})
	}
}

func TestSessionTypeDefinitionRoundTrip(t *testing.T) {
	target := uri.File("/src/target.go")

	tests := []struct {
		name   string
		result protocol.TypeDefinitionResult
		want   []Location
	}{
		{
			name:   "single location",
			result: &protocol.Location{URI: target, Range: targetRange()},
			want:   []Location{{File: "/src/target.go", Line: 5, Column: 3, EndLine: 5, EndColumn: 10}},
		},
		{
			name:   "location slice",
			result: protocol.LocationSlice{{URI: target, Range: targetRange()}},
			want:   []Location{{File: "/src/target.go", Line: 5, Column: 3, EndLine: 5, EndColumn: 10}},
		},
		{
			name:   "definition links",
			result: protocol.DefinitionLinkSlice{{TargetURI: target, TargetRange: targetRange(), TargetSelectionRange: targetRange()}},
			want:   []Location{{File: "/src/target.go", Line: 5, Column: 3, EndLine: 5, EndColumn: 10}},
		},
		{
			name:   "no result",
			result: nil,
			want:   []Location{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			fs := newFakeServer()
			fs.typeDefinitionResult = tt.result
			s, _, err := startFakeSession(ctx, t, fs, nil)
			if err != nil {
				t.Fatalf("start session: %v", err)
			}

			got, err := s.TypeDefinition(ctx, "/src/caller.go", 12, 4)
			if err != nil {
				t.Fatalf("type definition: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("type definition = %+v, want %+v", got, tt.want)
			}
			if methods := fs.recorded(); !slices.Contains(methods, "textDocument/typeDefinition") {
				t.Errorf("server did not receive type definition request, got %v", methods)
			}
		})
	}
}

func TestSessionImplementationCapabilityDeclared(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	_, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	caps := fs.initializeParams().Capabilities.TextDocument
	if caps.Implementation == nil {
		t.Fatal("Implementation capability not declared")
	}
	if caps.Implementation.LinkSupport == nil || !*caps.Implementation.LinkSupport {
		t.Error("Implementation LinkSupport not true")
	}
}

func TestSessionTypeDefinitionCapabilityDeclared(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	_, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	caps := fs.initializeParams().Capabilities.TextDocument
	if caps.TypeDefinition == nil {
		t.Fatal("TypeDefinition capability not declared")
	}
	if caps.TypeDefinition.LinkSupport == nil || !*caps.TypeDefinition.LinkSupport {
		t.Error("TypeDefinition LinkSupport not true")
	}
}

func TestSessionHandshakeHonoursContextDeadline(t *testing.T) {
	fs := newFakeServer()
	fs.stallInitialize()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, _, err := startFakeSession(ctx, t, fs, nil); err == nil {
		t.Fatal("expected handshake to fail on context deadline")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("handshake error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > testTimeout {
		t.Errorf("handshake took %v, want it bounded by the context deadline", elapsed)
	}
}

func TestSessionRequestFailsWhenServerExits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	fs.stallDefinition()
	s, proc, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	// The request context stays generous so a timeout cannot masquerade as an exit.
	callCtx, callCancel := context.WithTimeout(ctx, testTimeout)
	defer callCancel()

	errCh := make(chan error, 1)
	go func() {
		_, defErr := s.Definition(callCtx, "/src/caller.go", 1, 1)
		errCh <- defErr
	}()

	time.AfterFunc(20*time.Millisecond, proc.markExited)

	select {
	case defErr := <-errCh:
		if !errors.Is(defErr, errServerExited) {
			t.Fatalf("definition error = %v, want errServerExited", defErr)
		}
	case <-time.After(testTimeout):
		t.Fatal("definition did not return after the server exited")
	}
}

func TestSessionRequestsRejectedAfterServerExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	s, proc, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	proc.markExited()

	if _, err := s.Definition(ctx, "/src/caller.go", 1, 1); !errors.Is(err, errServerExited) {
		t.Errorf("definition error = %v, want errServerExited", err)
	}
	if err := s.DidClose(ctx, "/src/caller.go"); !errors.Is(err, errServerExited) {
		t.Errorf("did close error = %v, want errServerExited", err)
	}
}

func TestSessionCloseSendsShutdownBeforeExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	s, proc, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	if err := s.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case <-fs.exited:
	case <-time.After(testTimeout):
		t.Fatal("server never received exit")
	}

	methods := fs.recorded()
	shutdownAt := slices.Index(methods, "shutdown")
	exitAt := slices.Index(methods, "exit")
	if shutdownAt < 0 || exitAt < 0 {
		t.Fatalf("methods = %v, want both shutdown and exit", methods)
	}
	if shutdownAt > exitAt {
		t.Errorf("methods = %v, want shutdown before exit", methods)
	}
	if proc.wasKilled() {
		t.Error("a server that exited cleanly must not be force-terminated")
	}
}

func TestSessionCloseForceKillsUnresponsiveServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	fs.ignoreExit = true
	s, proc, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	closeErr := s.Close(ctx)
	if closeErr == nil {
		t.Fatal("expected close to report the unresponsive server")
	}
	if !proc.wasKilled() {
		t.Error("close did not force-terminate the unresponsive server")
	}
}

func TestSessionForwardsDiagnosticsAndProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	s, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	fs.notifyDiagnostics(ctx, t, &protocol.PublishDiagnosticsParams{
		URI:     uri.File("/src/broken.go"),
		Version: protocol.NewOptional(int32(7)),
		Diagnostics: []protocol.Diagnostic{{
			Range:    targetRange(),
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   protocol.NewOptional("gopls"),
			Code:     protocol.String("unusedvar"),
			Message:  protocol.String("declared and not used: x"),
		}},
	})

	select {
	case got := <-s.Diagnostics():
		want := PublishedDiagnostics{
			File:  "/src/broken.go",
			Items: []Diagnostic{{File: "/src/broken.go", Line: 5, Column: 3, Severity: "warning", Source: "gopls", Message: "declared and not used: x", Code: "unusedvar"}},
		}
		if got.File != want.File {
			t.Errorf("file = %q, want %q", got.File, want.File)
		}
		if got.Version == nil || *got.Version != 7 {
			t.Errorf("version = %v, want 7", got.Version)
		}
		if !slices.Equal(got.Items, want.Items) {
			t.Errorf("items = %+v, want %+v", got.Items, want.Items)
		}
	case <-time.After(testTimeout):
		t.Fatal("diagnostics never arrived")
	}

	begin, err := json.Marshal(protocol.WorkDoneProgressBegin{Kind: "begin", Title: "Loading packages"})
	if err != nil {
		t.Fatalf("marshal begin: %v", err)
	}
	fs.notifyProgress(ctx, t, &protocol.ProgressParams{
		Token: protocol.String("load"),
		Value: protocol.LSPAny(begin),
	})

	select {
	case got := <-s.Progress():
		want := ProgressEvent{Token: "load", Kind: "begin", Message: "Loading packages"}
		if got != want {
			t.Errorf("progress = %+v, want %+v", got, want)
		}
	case <-time.After(testTimeout):
		t.Fatal("progress event never arrived")
	}
}

func TestSessionProgressEventDecoding(t *testing.T) {
	message := "3/25 packages"

	tests := []struct {
		name  string
		value any
		token protocol.ProgressToken
		want  ProgressEvent
		ok    bool
	}{
		{
			name:  "begin falls back to title",
			value: protocol.WorkDoneProgressBegin{Kind: "begin", Title: "Loading packages"},
			token: protocol.String("load"),
			want:  ProgressEvent{Token: "load", Kind: "begin", Message: "Loading packages"},
			ok:    true,
		},
		{
			name:  "begin prefers message",
			value: protocol.WorkDoneProgressBegin{Kind: "begin", Title: "Loading packages", Message: &message},
			token: protocol.String("load"),
			want:  ProgressEvent{Token: "load", Kind: "begin", Message: message},
			ok:    true,
		},
		{
			name:  "report",
			value: protocol.WorkDoneProgressReport{Kind: "report", Message: &message},
			token: protocol.Integer(42),
			want:  ProgressEvent{Token: "42", Kind: "report", Message: message},
			ok:    true,
		},
		{
			name:  "end",
			value: protocol.WorkDoneProgressEnd{Kind: "end", Message: &message},
			token: protocol.String("load"),
			want:  ProgressEvent{Token: "load", Kind: "end", Message: message},
			ok:    true,
		},
		{
			name:  "unknown kind is dropped",
			value: map[string]string{"kind": "restart"},
			token: protocol.String("load"),
			ok:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatalf("marshal value: %v", err)
			}

			got, ok := decodeProgress(&protocol.ProgressParams{Token: tt.token, Value: protocol.LSPAny(raw)})
			if ok != tt.ok {
				t.Fatalf("decodeProgress ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("decodeProgress = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSessionForwardsInitializationOptions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	opts := map[string]any{
		"build": map[string]any{"directoryFilters": []any{"-node_modules"}},
		"ui":    map[string]any{"semanticTokens": true},
	}

	fs := newFakeServer()
	if _, _, err := startFakeSession(ctx, t, fs, opts); err != nil {
		t.Fatalf("start session: %v", err)
	}

	params := fs.initializeParams()
	if params == nil {
		t.Fatal("server did not record initialize params")
	}

	var got map[string]any
	if err := json.Unmarshal(params.InitializationOptions, &got); err != nil {
		t.Fatalf("decode initialization options %q: %v", params.InitializationOptions, err)
	}
	wantRaw, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	var want map[string]any
	if err := json.Unmarshal(wantRaw, &want); err != nil {
		t.Fatalf("decode want: %v", err)
	}
	if !mapsEqualJSON(got, want) {
		t.Errorf("initializationOptions = %+v, want %+v", got, want)
	}
}

func TestSessionOmitsEmptyInitializationOptions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	if _, _, err := startFakeSession(ctx, t, fs, nil); err != nil {
		t.Fatalf("start session: %v", err)
	}

	params := fs.initializeParams()
	if params == nil {
		t.Fatal("server did not record initialize params")
	}
	if len(params.InitializationOptions) != 0 {
		t.Errorf("initializationOptions = %q, want absent", params.InitializationOptions)
	}
}

func TestHoverContentsToText(t *testing.T) {
	tests := []struct {
		name     string
		contents protocol.HoverContents
		want     string
	}{
		{
			name:     "nil contents",
			contents: nil,
			want:     "",
		},
		{
			name: "markup content markdown",
			contents: &protocol.MarkupContent{
				Kind:  "markdown",
				Value: "# Title\n\nSome content",
			},
			want: "# Title\n\nSome content",
		},
		{
			name: "markup content plaintext",
			contents: &protocol.MarkupContent{
				Kind:  "plaintext",
				Value: "Plain text content",
			},
			want: "Plain text content",
		},
		{
			name:     "string (deprecated)",
			contents: protocol.String("plain string"),
			want:     "plain string",
		},
		{
			name: "marked string with language",
			contents: &protocol.MarkedStringWithLanguage{ //nolint:staticcheck // sent for older servers
				Language: "go",
				Value:    "func main() {}",
			},
			want: "```go\nfunc main() {}\n```",
		},
		{
			name: "marked string slice",
			contents: protocol.MarkedStringSlice{
				protocol.String("First item"),
				&protocol.MarkedStringWithLanguage{Language: "go", Value: "func() {}"}, //nolint:staticcheck // sent for older servers
			},
			want: "First item\n\n```go\nfunc() {}\n```",
		},
		{
			name:     "nil markup content",
			contents: (*protocol.MarkupContent)(nil),
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hoverContentsToText(tt.contents)
			if got != tt.want {
				t.Errorf("hoverContentsToText = %q, want %q", got, tt.want)
			}
		})
	}
}

func strPtr(s string) *string { return &s }

func TestSessionWorkspaceSymbolFlat(t *testing.T) {
	target := uri.File("/src/target.go")

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	fs.workspaceSymbolResult = protocol.SymbolInformationSlice{
		{
			BaseSymbolInformation: protocol.BaseSymbolInformation{
				Name:          "Foo",
				Kind:          protocol.SymbolKindFunction,
				ContainerName: strPtr("pkg"),
			},
			Location: protocol.Location{URI: target, Range: targetRange()},
		},
	}

	s, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	got, err := s.WorkspaceSymbol(ctx, "Foo")
	if err != nil {
		t.Fatalf("WorkspaceSymbol: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("WorkspaceSymbol: got %d symbols, want 1", len(got))
	}
	want := SymbolInfo{
		Location:  Location{File: "/src/target.go", Line: 5, Column: 3, EndLine: 5, EndColumn: 10},
		Name:      "Foo",
		Kind:      "function",
		Container: "pkg",
	}
	if got[0] != want {
		t.Errorf("WorkspaceSymbol: got %+v, want %+v", got[0], want)
	}
}

func TestSessionWorkspaceSymbolWorkspaceSymbolSlice(t *testing.T) {
	target := uri.File("/src/target.go")

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	fs.workspaceSymbolResult = protocol.WorkspaceSymbolSlice{
		{
			BaseSymbolInformation: protocol.BaseSymbolInformation{
				Name: "Bar",
				Kind: protocol.SymbolKindStruct,
			},
			Location: &protocol.Location{URI: target, Range: targetRange()},
		},
	}

	s, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	got, err := s.WorkspaceSymbol(ctx, "Bar")
	if err != nil {
		t.Fatalf("WorkspaceSymbol: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("WorkspaceSymbol: got %d symbols, want 1", len(got))
	}
	if got[0].Name != "Bar" || got[0].Kind != "struct" || got[0].Line != 5 || got[0].Column != 3 {
		t.Errorf("WorkspaceSymbol: unexpected result %+v", got[0])
	}
}

func TestSessionWorkspaceSymbolLocationUriOnly(t *testing.T) {
	target := uri.File("/src/target.go")

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	fs.workspaceSymbolResult = protocol.WorkspaceSymbolSlice{
		{
			BaseSymbolInformation: protocol.BaseSymbolInformation{
				Name: "Baz",
				Kind: protocol.SymbolKindVariable,
			},
			Location: &protocol.LocationUriOnly{URI: target},
			// Data disambiguates the wire encoding from SymbolInformationSlice:
			// without it, a bare LocationUriOnly-only WorkspaceSymbol element is
			// shape-identical to a SymbolInformation element and the generated
			// decoder resolves the ambiguity in favor of SymbolInformationSlice.
			Data: protocol.LSPAny(`1`),
		},
	}

	s, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	got, err := s.WorkspaceSymbol(ctx, "Baz")
	if err != nil {
		t.Fatalf("WorkspaceSymbol: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("WorkspaceSymbol: got %d symbols, want 1", len(got))
	}
	want := SymbolInfo{
		Location: Location{File: "/src/target.go", Line: 1, Column: 1},
		Name:     "Baz",
		Kind:     "var",
	}
	if got[0] != want {
		t.Errorf("WorkspaceSymbol (LocationUriOnly): got %+v, want %+v", got[0], want)
	}
}

func TestSessionDocumentSymbolFlat(t *testing.T) {
	target := uri.File("/src/target.go")

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	fs.documentSymbolResult = protocol.SymbolInformationSlice{
		{
			BaseSymbolInformation: protocol.BaseSymbolInformation{
				Name: "Foo",
				Kind: protocol.SymbolKindFunction,
			},
			Location: protocol.Location{URI: target, Range: targetRange()},
		},
	}

	s, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	got, err := s.DocumentSymbol(ctx, "/src/target.go")
	if err != nil {
		t.Fatalf("DocumentSymbol: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Foo" {
		t.Fatalf("DocumentSymbol: unexpected result %+v", got)
	}
}

func TestSessionDocumentSymbolNestedChildren(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	fs.documentSymbolResult = protocol.DocumentSymbolSlice{
		{
			Name:  "Manager",
			Kind:  protocol.SymbolKindStruct,
			Range: targetRange(),
			Children: []protocol.DocumentSymbol{
				{
					Name:  "Close",
					Kind:  protocol.SymbolKindMethod,
					Range: targetRange(),
				},
			},
		},
	}

	s, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	got, err := s.DocumentSymbol(ctx, "/src/manager.go")
	if err != nil {
		t.Fatalf("DocumentSymbol: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("DocumentSymbol: got %d symbols, want 2", len(got))
	}
	if got[0].Name != "Manager" || got[0].Container != "" {
		t.Errorf("DocumentSymbol[0]: got %+v, want Name=Manager Container=\"\"", got[0])
	}
	if got[1].Name != "Close" || got[1].Container != "Manager" {
		t.Errorf("DocumentSymbol[1]: got %+v, want Name=Close Container=Manager", got[1])
	}
	if got[1].File != "/src/manager.go" {
		t.Errorf("DocumentSymbol[1].File: got %q, want /src/manager.go", got[1].File)
	}
}

func TestSessionDocumentSymbolEmptyResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	fs := newFakeServer()
	fs.documentSymbolResult = protocol.DocumentSymbolSlice{}

	s, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	got, err := s.DocumentSymbol(ctx, "/src/empty.go")
	if err != nil {
		t.Fatalf("DocumentSymbol: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("DocumentSymbol: got %d symbols, want 0", len(got))
	}
}

// mapsEqualJSON compares two decoded JSON objects.
func mapsEqualJSON(a, b map[string]any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(left) == string(right)
}
