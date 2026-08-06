package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// readConfigPatch reads a config file and returns it as a patch.
func readConfigPatch(path string, env map[string]string, allowMissing bool) (configPatch, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if allowMissing {
				return configPatch{}, nil
			}
			return configPatch{}, fmt.Errorf("config path %q does not exist", path)
		}
		return configPatch{}, fmt.Errorf("read config %q: %w", path, err)
	}

	root, err := decodeConfigNode(path, string(contents))
	if err != nil {
		return configPatch{}, err
	}

	if root.Kind == 0 {
		return configPatch{}, nil
	}

	lookup := func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
	missing := expandNodeTree(root, lookup)
	if len(missing) > 0 {
		return configPatch{}, undefinedVarError(path, missing)
	}

	cleaned, err := marshalCleanConfigNode(path, root)
	if err != nil {
		return configPatch{}, err
	}
	var patch configPatch
	if err := decodeKnownConfigPatch(cleaned, &patch); err != nil {
		return configPatch{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	return patch, nil
}

func undefinedVarError(path string, missing []undefinedVar) error {
	sort.Slice(missing, func(i, j int) bool {
		if missing[i].Line != missing[j].Line {
			return missing[i].Line < missing[j].Line
		}
		if missing[i].Path != missing[j].Path {
			return missing[i].Path < missing[j].Path
		}
		return missing[i].Name < missing[j].Name
	})

	var b strings.Builder
	fmt.Fprintf(&b, "config %q references undefined environment variables:\n", path)
	for _, uv := range missing {
		fmt.Fprintf(&b, "  line %-5d  %-40s  %s\n", uv.Line, uv.Path, uv.Name)
	}
	b.WriteString("use ${VAR:-} to allow an empty value, or $$VAR for a literal dollar sign\n")
	return fmt.Errorf("expand env vars: %s", strings.TrimSpace(b.String()))
}

func decodeConfigNode(path, contents string) (*yaml.Node, error) {
	var rawMapping yaml.Node
	if err := yaml.Unmarshal([]byte(contents), &rawMapping); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	return &rawMapping, nil
}

func marshalCleanConfigNode(path string, rawMapping *yaml.Node) ([]byte, error) {
	cleaned, err := yaml.Marshal(rawMapping)
	if err != nil {
		return nil, fmt.Errorf("marshal cleaned config %q: %w", path, err)
	}
	return cleaned, nil
}

func decodeKnownConfigPatch(cleaned []byte, patch *configPatch) error {
	dec := yaml.NewDecoder(strings.NewReader(string(cleaned)))
	dec.KnownFields(true)
	return dec.Decode(patch)
}
