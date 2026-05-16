package config

import (
	"errors"
	"fmt"
	"os"
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

	return parseConfigPatch(path, expandConfigContents(string(contents), env))
}

func expandConfigContents(contents string, env map[string]string) string {
	return expandEnvText(contents, func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	})
}

func parseConfigPatch(path, contents string) (configPatch, error) {
	rawMapping, err := decodeConfigNode(path, contents)
	if err != nil {
		return configPatch{}, err
	}

	cleaned, err := marshalCleanConfigNode(path, rawMapping)
	if err != nil {
		return configPatch{}, err
	}
	var patch configPatch
	if err := decodeKnownConfigPatch(cleaned, &patch); err != nil {
		return configPatch{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	return patch, nil
}

func decodeConfigNode(path, contents string) (*yaml.Node, error) {
	var rawMapping yaml.Node
	if err := yaml.Unmarshal([]byte(contents), &rawMapping); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	return &rawMapping, nil
}

func configRootNode(rawMapping *yaml.Node) *yaml.Node {
	if rawMapping.Kind == yaml.DocumentNode && len(rawMapping.Content) > 0 {
		return rawMapping.Content[0]
	}
	return rawMapping
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
