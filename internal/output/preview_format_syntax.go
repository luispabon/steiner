package output

import (
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

func DetectPreviewSyntax(path, contents string) PreviewSyntax {
	lexer := detectPreviewLexer(path, contents)
	return PreviewSyntax{
		Lexer:    lexer,
		Language: previewLanguageForLexer(lexer),
	}
}

func detectPreviewLexer(path, contents string) chroma.Lexer {
	if isBinaryLike(contents) {
		return chroma.Coalesce(lexers.Fallback)
	}
	if base := strings.TrimSpace(filepath.Base(path)); base != "" {
		if lexer := lexers.Match(base); lexer != nil {
			return chroma.Coalesce(lexer)
		}
	}
	if lexer := lexers.Analyse(contents); lexer != nil {
		return chroma.Coalesce(lexer)
	}
	return chroma.Coalesce(lexers.Fallback)
}

func previewLanguageForLexer(lexer chroma.Lexer) string {
	if lexer == nil || lexer.Config() == nil {
		return "plain"
	}
	name := strings.TrimSpace(lexer.Config().Name)
	if name == "" || strings.EqualFold(name, "fallback") {
		return "plain"
	}
	return strings.ToLower(name)
}
