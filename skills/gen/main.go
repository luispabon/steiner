// Command gen expands include directives in <name>/SKILL.md.src fixtures
// and writes the resulting <name>/SKILL.md files. It is invoked via
// go:generate from skills/embed.go and must be run with a working
// directory of skills/.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var includePattern = regexp.MustCompile(`^partials/[a-z0-9]+(-[a-z0-9]+)*\.md$`)

const includePrefix = "<!-- include: "
const includeSuffix = " -->"

func main() {
	if err := run("."); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root string) error {
	sources, err := findSources(root)
	if err != nil {
		return err
	}

	for _, src := range sources {
		if err := expandSource(root, src); err != nil {
			return err
		}
	}

	return nil
}

// findSources returns the sorted list of <name>/SKILL.md.src paths
// (relative to root) found directly under root.
func findSources(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read skills dir: %w", err)
	}

	var sources []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		src := filepath.Join(entry.Name(), "SKILL.md.src")
		if _, err := os.Stat(filepath.Join(root, src)); err == nil {
			sources = append(sources, src)
		}
	}
	sort.Strings(sources)

	return sources, nil
}

// expandSource reads the source file at root/src, expands its include
// directives, and writes the result to root/<name>/SKILL.md.
func expandSource(root, src string) error {
	data, err := os.ReadFile(filepath.Join(root, src))
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}

	partialsDir := filepath.Join(root, "partials")
	expanded, err := expand(string(data), partialsDir, src)
	if err != nil {
		return err
	}

	dst := filepath.Join(root, filepath.Dir(src), "SKILL.md")
	if err := os.WriteFile(dst, []byte(expanded), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}

	return nil
}

// expand splices include directives found in content with the contents
// of the referenced partial files, resolved against partialsDir. srcName
// is used only for error messages.
func expand(content, partialsDir, srcName string) (string, error) {
	lines := splitKeepEnds(content)

	var out strings.Builder
	for _, line := range lines {
		text, ending := splitLineEnding(line)
		path, isDirective := parseDirective(text)
		if !isDirective {
			out.WriteString(line)
			continue
		}

		partial, err := readPartial(partialsDir, path, srcName)
		if err != nil {
			return "", err
		}

		out.WriteString(partial)
		out.WriteString(ending)
	}

	return out.String(), nil
}

// parseDirective reports whether text is exactly an include directive
// line, returning its path. Any line resembling a directive that fails
// validation is treated as a directive whose path is invalid, so callers
// can surface an error rather than passing it through.
func parseDirective(text string) (path string, isDirective bool) {
	if len(text) < len(includePrefix)+len(includeSuffix) {
		return "", false
	}
	if !strings.HasPrefix(text, includePrefix) || !strings.HasSuffix(text, includeSuffix) {
		return "", false
	}

	path = text[len(includePrefix) : len(text)-len(includeSuffix)]
	return path, true
}

func readPartial(partialsDir, path, srcName string) (string, error) {
	if strings.Contains(path, "..") || strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("%s: invalid include path %q", srcName, path)
	}
	if !includePattern.MatchString(path) {
		return "", fmt.Errorf("%s: invalid include path %q", srcName, path)
	}

	name := strings.TrimPrefix(path, "partials/")
	data, err := os.ReadFile(filepath.Join(partialsDir, name))
	if err != nil {
		return "", fmt.Errorf("%s: read partial %q: %w", srcName, path, err)
	}

	body := strings.TrimSuffix(string(data), "\n")
	if hasDirective(body) {
		return "", fmt.Errorf("partial %q contains a nested include directive", path)
	}

	return body, nil
}

// hasDirective reports whether any line of body is an include directive.
func hasDirective(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if _, ok := parseDirective(line); ok {
			return true
		}
	}
	return false
}

// splitKeepEnds splits s into lines, preserving each line's trailing
// "\n" (if present) as part of the returned element.
func splitKeepEnds(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// splitLineEnding separates a line (as returned by splitKeepEnds) into
// its text and trailing newline, if any.
func splitLineEnding(line string) (text, ending string) {
	if strings.HasSuffix(line, "\n") {
		return line[:len(line)-1], "\n"
	}
	return line, ""
}
