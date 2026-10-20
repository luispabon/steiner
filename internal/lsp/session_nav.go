package lsp

import (
	"context"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// Definition requests the definition of a symbol at the given position.
//
//nolint:dupl // identical to Implementation and TypeDefinition; mechanical clone across protocol.DefinitionResult-aliased types
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
//
//nolint:dupl // identical to Definition and TypeDefinition; mechanical clone across protocol.DefinitionResult-aliased types
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
//
//nolint:dupl // identical to Definition and Implementation; mechanical clone across protocol.DefinitionResult-aliased types
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
