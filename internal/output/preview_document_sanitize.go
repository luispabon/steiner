package output

import "slices"

// SanitizePreviewDocument returns doc with its path, language, line prefixes,
// and span text passed through SanitizeDisplayText. Lines and spans are copied
// so the caller's document is unchanged. Use it at render time for documents
// that were not built by the normalizing formatters, such as display_file
// placeholder previews.
func SanitizePreviewDocument(doc PreviewDocument) PreviewDocument {
	doc.Path = SanitizeDisplayText(doc.Path)
	doc.Language = SanitizeDisplayText(doc.Language)
	doc.Lines = slices.Clone(doc.Lines)
	for i := range doc.Lines {
		doc.Lines[i].Prefix = SanitizeDisplayText(doc.Lines[i].Prefix)
		doc.Lines[i].Spans = slices.Clone(doc.Lines[i].Spans)
		for j := range doc.Lines[i].Spans {
			doc.Lines[i].Spans[j].Text = SanitizeDisplayText(doc.Lines[i].Spans[j].Text)
		}
	}
	return doc
}
