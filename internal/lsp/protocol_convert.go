package lsp

import (
	"fmt"
	"strings"

	"go.lsp.dev/protocol"
)

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
