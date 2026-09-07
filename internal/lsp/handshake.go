package lsp

import (
	"context"
	"encoding/json"
	"fmt"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// handshake performs the LSP initialize/initialized sequence and returns the
// initialize result. rootPath must be an absolute directory path, and initOpts
// is forwarded verbatim as the server's initializationOptions.
func handshake(ctx context.Context, server protocol.Server, rootPath string, initOpts map[string]any) (*protocol.InitializeResult, error) {
	rootURI := uri.File(rootPath)

	initParams := protocol.InitializeParams{
		ProcessID: nil,
		// rootPath and rootUri are deprecated in favour of workspaceFolders, but
		// servers that predate it still rely on them.
		RootPath: protocol.NewNullable(rootPath), //nolint:staticcheck // sent for older servers
		RootURI:  &rootURI,                       //nolint:staticcheck // sent for older servers
		WorkspaceFoldersInitializeParams: protocol.WorkspaceFoldersInitializeParams{
			WorkspaceFolders: protocol.NewNullable([]protocol.WorkspaceFolder{
				{
					URI:  rootURI,
					Name: "root",
				},
			}),
		},
		Capabilities: protocol.ClientCapabilities{
			TextDocument: &protocol.TextDocumentClientCapabilities{
				Definition: &protocol.DefinitionClientCapabilities{
					LinkSupport: ptrBool(true),
				},
				References: &protocol.ReferenceClientCapabilities{},
				PublishDiagnostics: &protocol.PublishDiagnosticsClientCapabilities{
					DiagnosticsCapabilities: protocol.DiagnosticsCapabilities{},
				},
			},
			Window: &protocol.WindowClientCapabilities{
				WorkDoneProgress: ptrBool(true),
			},
		},
	}

	if len(initOpts) > 0 {
		raw, err := json.Marshal(initOpts)
		if err != nil {
			return nil, fmt.Errorf("encode initialization options: %w", err)
		}
		initParams.InitializationOptions = protocol.LSPAny(raw)
	}

	initResult, err := server.Initialize(ctx, &initParams)
	if err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if initResult == nil {
		return nil, fmt.Errorf("initialize: nil result")
	}

	if err := server.Initialized(ctx, &protocol.InitializedParams{}); err != nil {
		return nil, fmt.Errorf("initialized notification: %w", err)
	}

	return initResult, nil
}

func ptrBool(v bool) *bool {
	return &v
}
