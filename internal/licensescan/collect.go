package licensescan

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// licenseTokens are the basename tokens that mark a file as a license or
// notice file.
var licenseTokens = map[string]struct{}{
	"license":   {},
	"licence":   {},
	"copying":   {},
	"notice":    {},
	"notices":   {},
	"copyright": {},
	"unlicense": {},
	"patents":   {},
}

// licenseExtensions are the file extensions accepted for license files. The
// empty extension is allowed, so a plain LICENSE is picked up.
var licenseExtensions = map[string]struct{}{
	"":          {},
	".txt":      {},
	".md":       {},
	".markdown": {},
	".rst":      {},
}

// collectLicenseFiles returns the license and notice files for a module: the
// files in the module root and in every directory between a compiled package
// directory and the module root. Scan only passes real package directories, so
// source files, test files and testdata trees never contribute.
func collectLicenseFiles(moduleDir string, packageDirs []string) ([]LicenseFile, error) {
	root, err := filepath.Abs(moduleDir)
	if err != nil {
		return nil, fmt.Errorf("resolve module directory: %w", err)
	}
	found := map[string]struct{}{}
	dirs := append([]string{root}, packageDirs...)
	for _, dir := range dirs {
		if err := collectUp(root, dir, found); err != nil {
			return nil, err
		}
	}

	paths := make([]string, 0, len(found))
	for path := range found {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	files := make([]LicenseFile, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		files = append(files, LicenseFile{Path: path, Text: normaliseText(string(data))})
	}
	return files, nil
}

// collectUp records the license files in dir and its ancestors up to root.
func collectUp(root, dir string, found map[string]struct{}) error {
	current, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve package directory %s: %w", dir, err)
	}
	for {
		entries, err := os.ReadDir(current)
		if err != nil {
			return fmt.Errorf("read %s: %w", current, err)
		}
		for _, entry := range entries {
			if !entry.Type().IsRegular() || !isLicenseName(entry.Name()) {
				continue
			}
			rel, err := filepath.Rel(root, filepath.Join(current, entry.Name()))
			if err != nil {
				return fmt.Errorf("resolve license file path: %w", err)
			}
			rel = filepath.ToSlash(rel)
			if rel == ".." || strings.HasPrefix(rel, "../") {
				continue
			}
			found[rel] = struct{}{}
		}
		if current == root {
			return nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

// isLicenseName reports whether name is a license or notice file.
func isLicenseName(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	if _, ok := licenseExtensions[ext]; !ok {
		return false
	}
	base := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	for _, token := range strings.FieldsFunc(base, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	}) {
		if _, ok := licenseTokens[token]; ok {
			return true
		}
	}
	return false
}

// normaliseText converts CRLF line endings to LF.
func normaliseText(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}
