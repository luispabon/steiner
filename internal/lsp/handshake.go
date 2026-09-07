package lsp

import (
	"context"
	"fmt"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// handshake performs the LSP initialize/initialized sequence.
// rootPath must be an absolute directory path.
func handshake(ctx context.Context, conn jsonrpc2.Conn, server protocol.Server, rootPath string) error {
	rootURI := uri.File(rootPath)

	initParams := protocol.InitializeParams{
		ProcessID: nil,
		RootPath:  protocol.NewNullable(rootPath),
		RootURI:   &rootURI,
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

	// Send initialize request
	initResult, err := server.Initialize(ctx, &initParams)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if initResult == nil {
		return fmt.Errorf("initialize: nil result")
	}

	// Send initialized notification
	if err := server.Initialized(ctx, &protocol.InitializedParams{}); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}

	return nil
}

func ptrBool(v bool) *bool {
	return &v
}
