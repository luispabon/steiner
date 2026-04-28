// This file incorporates work from Dive (https://github.com/deepnoodle-ai/dive)
// which is licensed under Apache 2.0. The original work has been modified.
//
// Copyright 2024 DeepNoodle Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package builtin

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gobwas/glob"
	"github.com/luispabon/steiner/internal/tool"
)

type grepMatch struct {
	file       string
	lineNumber int
	line       string
}

var grepTypeToExt = map[string][]string{
	"go":     {".go"},
	"ts":     {".ts", ".tsx"},
	"js":     {".js", ".jsx"},
	"py":     {".py"},
	"rust":   {".rs"},
	"java":   {".java"},
	"c":      {".c", ".h"},
	"cpp":    {".cpp", ".cc", ".cxx", ".hpp", ".hh"},
	"rb":     {".rb"},
	"php":    {".php"},
	"swift":  {".swift"},
	"kotlin": {".kt", ".kts"},
	"scala":  {".scala"},
	"md":     {".md", ".markdown"},
	"json":   {".json"},
	"yaml":   {".yaml", ".yml"},
	"xml":    {".xml"},
	"html":   {".html", ".htm"},
	"css":    {".css", ".scss", ".sass", ".less"},
	"sql":    {".sql"},
	"sh":     {".sh", ".bash"},
}

func grepSearch(ctx context.Context, root, pattern string, caseInsens, multiline bool, fileGlob, fileType string, excluder *tool.PathExcluder, limit int) ([]grepMatch, error) {
	p := pattern
	if caseInsens {
		p = "(?i)" + p
	}
	if multiline {
		p = "(?s)" + p
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	var filterGlob glob.Glob
	if fileGlob != "" {
		filterGlob, err = glob.Compile(fileGlob, '/')
		if err != nil {
			return nil, fmt.Errorf("invalid glob: %w", err)
		}
	}

	var exts []string
	if fileType != "" {
		exts = grepTypeToExt[fileType]
	}

	var matches []grepMatch

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return filepath.SkipAll
		default:
		}

		relPath, _ := filepath.Rel(root, path)
		relPath = filepath.ToSlash(relPath)

		if excluder != nil && excluder.ShouldExclude(relPath) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}

		if filterGlob != nil && !filterGlob.Match(relPath) {
			return nil
		}

		if exts != nil {
			ext := filepath.Ext(path)
			found := false
			for _, e := range exts {
				if strings.EqualFold(ext, e) {
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		checkLen := 512
		if len(content) < checkLen {
			checkLen = len(content)
		}
		if bytes.Contains(content[:checkLen], []byte{0}) {
			return nil
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				matches = append(matches, grepMatch{
					file:       relPath,
					lineNumber: i + 1,
					line:       strings.TrimRight(line, "\r"),
				})
				if len(matches) >= limit {
					return filepath.SkipAll
				}
			}
		}

		return nil
	})

	if walkErr != nil && walkErr != filepath.SkipAll {
		return nil, fmt.Errorf("walk: %w", walkErr)
	}

	return matches, nil
}

func formatGrepOutput(matches []grepMatch, mode string, showLines bool) string {
	if len(matches) == 0 {
		return "No matches found"
	}

	var b strings.Builder

	switch mode {
	case "files_with_matches":
		seen := make(map[string]bool)
		var files []string
		for _, m := range matches {
			if !seen[m.file] {
				seen[m.file] = true
				files = append(files, m.file)
			}
		}
		sort.Strings(files)
		for _, f := range files {
			b.WriteString(f)
			b.WriteString("\n")
		}

	case "count":
		counts := make(map[string]int)
		for _, m := range matches {
			counts[m.file]++
		}
		var files []string
		for f := range counts {
			files = append(files, f)
		}
		sort.Strings(files)
		for _, f := range files {
			b.WriteString(fmt.Sprintf("%s:%d\n", f, counts[f]))
		}

	case "content":
		fallthrough
	default:
		byFile := make(map[string][]grepMatch)
		var files []string
		for _, m := range matches {
			if _, ok := byFile[m.file]; !ok {
				files = append(files, m.file)
			}
			byFile[m.file] = append(byFile[m.file], m)
		}
		sort.Strings(files)

		for _, f := range files {
			b.WriteString(fmt.Sprintf("## %s\n", f))
			for _, m := range byFile[f] {
				if showLines {
					b.WriteString(fmt.Sprintf("%d: %s\n", m.lineNumber, m.line))
				} else {
					b.WriteString(fmt.Sprintf("%s\n", m.line))
				}
			}
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
}
