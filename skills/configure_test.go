package skills_test

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/skill"
	"github.com/luispabon/steiner/skills"
)

const configureReferenceHeading = "## Configure Skill Reference"

const maxConfigureReferenceBytes = 12_288

func TestConfigureSkillIsEmbeddedAndCoversCanonicalConfig(t *testing.T) {
	loader := skill.Loader{BundledFS: skills.FS}
	discovered, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover bundled skills: %v", err)
	}

	var found bool
	for _, discoveredSkill := range discovered {
		if discoveredSkill.Name == "configure" {
			found = true
			if discoveredSkill.Source != "bundled" {
				t.Fatalf("configure skill source = %q, want bundled", discoveredSkill.Source)
			}
			break
		}
	}
	if !found {
		t.Fatal("bundled skill discovery did not find configure")
	}

	loaded, err := loader.Load(context.Background(), "configure")
	if err != nil {
		t.Fatalf("load configure skill: %v", err)
	}
	skillReference, err := extractConfigureReference(loaded.Content)
	if err != nil {
		t.Fatalf("extract configure skill reference: %v", err)
	}
	if len(skillReference) > maxConfigureReferenceBytes {
		t.Fatalf("configure skill reference is %d bytes, want <= %d", len(skillReference), maxConfigureReferenceBytes)
	}

	referencePaths := extractReferencePaths(skillReference)
	if len(referencePaths) == 0 {
		t.Fatal("canonical reference contains no path rows")
	}
	missing := missingPaths(canonicalConfigPaths(), referencePaths)
	if len(missing) != 0 {
		t.Fatalf("canonical reference omits config paths: %#v", missing)
	}
}

func TestConfigureReferenceHeadingValidation(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "missing", content: "# Configuration\n\n## Other\n"},
		{name: "duplicate", content: configureReferenceHeading + "\n\n## Other\n\n" + configureReferenceHeading + "\n"},
		{name: "mismatched", content: "## Configure Skill References\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := extractConfigureReference(test.content); err == nil {
				t.Fatal("extractConfigureReference() error = nil, want error")
			}
		})
	}
}

func extractConfigureReference(content string) (string, error) {
	var starts []int
	offset := 0
	for _, line := range strings.SplitAfter(content, "\n") {
		lineText := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if lineText == configureReferenceHeading {
			starts = append(starts, offset)
		}
		offset += len(line)
	}
	if len(starts) != 1 {
		return "", fmt.Errorf("found %d exact %q headings, want 1", len(starts), configureReferenceHeading)
	}

	start := starts[0]
	end := len(content)
	offset = 0
	for _, line := range strings.SplitAfter(content[start:], "\n") {
		lineText := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if offset != 0 && strings.HasPrefix(lineText, "## ") {
			end = start + offset
			break
		}
		offset += len(line)
	}
	return content[start:end], nil
}

func extractReferencePaths(section string) map[string]struct{} {
	paths := make(map[string]struct{})
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		columns := strings.Split(line, "|")
		if len(columns) < 3 {
			continue
		}
		path := strings.TrimSpace(columns[1])
		if len(path) < 2 || path[0] != '`' || path[len(path)-1] != '`' {
			continue
		}
		paths[normalizePath(path[1:len(path)-1])] = struct{}{}
	}
	return paths
}

func canonicalConfigPaths() []string {
	paths := make(map[string]struct{})
	collectConfigPaths(reflect.TypeOf(config.Config{}), "", paths)
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func collectConfigPaths(typ reflect.Type, path string, paths map[string]struct{}) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	switch typ.Kind() {
	case reflect.Struct:
		fields := 0
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			if field.PkgPath != "" {
				continue
			}
			name := strings.Split(field.Tag.Get("yaml"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = strings.ToLower(field.Name)
			}
			fields++
			collectConfigPaths(field.Type, joinPath(path, name), paths)
		}
		if fields == 0 && path != "" {
			paths[normalizePath(path)] = struct{}{}
		}
	case reflect.Map:
		if typ.Key().Kind() != reflect.String || path == "" {
			return
		}
		entryPath := joinPath(path, "<name>")
		element := typ.Elem()
		for element.Kind() == reflect.Pointer {
			element = element.Elem()
		}
		if element.Kind() == reflect.Interface {
			paths[normalizePath(path)] = struct{}{}
			return
		}
		collectConfigPaths(element, entryPath, paths)
	case reflect.Slice, reflect.Array:
		if path == "" {
			return
		}
		element := typ.Elem()
		for element.Kind() == reflect.Pointer {
			element = element.Elem()
		}
		if element.Kind() == reflect.Struct && structHasExportedYAMLFields(element) {
			collectConfigPaths(element, joinPath(path, "<index>"), paths)
			return
		}
		paths[normalizePath(path)] = struct{}{}
	default:
		if path != "" {
			paths[normalizePath(path)] = struct{}{}
		}
	}
}

func structHasExportedYAMLFields(typ reflect.Type) bool {
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.PkgPath == "" && strings.Split(field.Tag.Get("yaml"), ",")[0] != "-" {
			return true
		}
	}
	return false
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func normalizePath(path string) string {
	parts := strings.Split(path, ".")
	for index, part := range parts {
		if strings.HasPrefix(part, "<") && strings.HasSuffix(part, ">") {
			parts[index] = "<name>"
		}
	}
	return strings.Join(parts, ".")
}

func missingPaths(expected []string, actual map[string]struct{}) []string {
	var missing []string
	for _, path := range expected {
		if _, ok := actual[normalizePath(path)]; !ok {
			missing = append(missing, path)
		}
	}
	return missing
}
