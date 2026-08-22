package prompt

import (
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// promptFuncs are the generic helpers available to every preamble template.
// They stay deliberately small: list/append build an ordered list whose
// optional entries splice in mid-sequence, add1 numbers a range 1-based, and
// join exposes strings.Join.
var promptFuncs = template.FuncMap{
	"list": func(items ...string) []string {
		return items
	},
	"append": func(items []string, extra ...string) []string {
		out := make([]string, 0, len(items)+len(extra))
		out = append(out, items...)
		return append(out, extra...)
	},
	"add1": func(i int) int {
		return i + 1
	},
	"join": strings.Join,
}

var promptTemplates = template.Must(template.New("prompt").Funcs(promptFuncs).ParseFS(templatesFS, "templates/*.tmpl"))

// renderTemplate executes an embedded preamble template. Templates and their
// data shapes are compile-time constants of this package, so an execution
// failure is a programmer error rather than a runtime condition; it is
// surfaced immediately instead of silently emitting a truncated preamble.
func renderTemplate(name string, data any) string {
	var b strings.Builder
	if err := promptTemplates.ExecuteTemplate(&b, name, data); err != nil {
		panic(fmt.Errorf("render preamble template %s: %w", name, err))
	}
	return b.String()
}
