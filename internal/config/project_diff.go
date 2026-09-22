package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// FieldChange is one leaf setting the project config overrides.
type FieldChange struct {
	Path     string // dotted YAML path, e.g. "mcp.servers.foo.command"
	Before   string // rendered global/default value, "(unset)", or "(set)" when masked
	After    string // rendered project value, unexpanded, never masked
	Security bool   // matches the security-relevant field set
}

// ProjectInspection describes the project config layer before it is trusted.
type ProjectInspection struct {
	ProjectRoot string        // absolute, symlink-resolved working directory (trust key)
	ConfigPath  string        // project config file path; "" when no file exists
	Trusted     bool          // ProjectRoot is recorded in the trust store
	Changes     []FieldChange // sorted: Security first, then Path ascending
	ParseError  string        // non-empty when the project YAML could not be parsed
	StoreNotice string        // non-empty when a corrupt trust store was deleted
}

// InspectProject reads the project config layer without expanding environment
// variables or validating it, and reports how it would change the global config.
// It deletes a corrupt trust store (see StoreNotice). It never writes trust.
func InspectProject(opts LoadOptions) (ProjectInspection, error) {
	env := loadEnvironment(opts.Env)
	homeDir, err := resolveHomeDir(opts.HomeDir, env)
	if err != nil {
		return ProjectInspection{}, err
	}
	workingDir, err := resolveWorkingDir(opts.WorkingDir)
	if err != nil {
		return ProjectInspection{}, err
	}

	root, err := resolveProjectRoot(workingDir)
	if err != nil {
		return ProjectInspection{}, err
	}

	paths := resolveConfigPaths(opts, homeDir, workingDir)
	global, project := paths[0], paths[1]

	store, notice, err := readTrustStore(trustStorePath(homeDir))
	if err != nil {
		return ProjectInspection{}, err
	}

	inspection := ProjectInspection{
		ProjectRoot: root,
		Trusted:     store.isTrusted(root),
		StoreNotice: notice,
	}

	if project.path == "" {
		return inspection, nil
	}

	switch _, statErr := os.Stat(project.path); {
	case statErr == nil:
		inspection.ConfigPath = project.path
	case os.IsNotExist(statErr):
		if project.allowMissing {
			return inspection, nil
		}
		return ProjectInspection{}, fmt.Errorf("config path %q does not exist", project.path)
	default:
		return ProjectInspection{}, fmt.Errorf("stat config %q: %w", project.path, statErr)
	}

	projectNode, err := readRawConfigNode(project.path)
	if err != nil {
		inspection.ParseError = err.Error()
		return inspection, nil
	}

	globalNode := readRawConfigNodeOrNil(global.path)
	defaultsNode := defaultConfigNodeOrNil(env)

	inspection.Changes = diffProject(projectNode, globalNode, defaultsNode)
	return inspection, nil
}

// resolveProjectRoot returns the absolute, symlink-resolved form of
// workingDir, the trust store's lookup key.
func resolveProjectRoot(workingDir string) (string, error) {
	abs, err := filepath.Abs(workingDir)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	return resolved, nil
}

// readRawConfigNode reads and raw-decodes path, unwrapping the YAML document
// node down to its root mapping. No env expansion or validation is applied.
// It returns (nil, nil) for an empty document.
func readRawConfigNode(path string) (*yaml.Node, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	node, err := decodeConfigNode(path, string(contents))
	if err != nil {
		return nil, err
	}
	return unwrapDocument(node), nil
}

// readRawConfigNodeOrNil is readRawConfigNode with missing files and parse
// errors treated as an absent layer rather than reported.
func readRawConfigNodeOrNil(path string) *yaml.Node {
	if path == "" {
		return nil
	}
	node, err := readRawConfigNode(path)
	if err != nil {
		return nil
	}
	return node
}

// defaultConfigNodeOrNil renders defaultConfig(env) to a raw YAML node for use
// as the fallback "before" source. Any failure yields a nil node.
func defaultConfigNodeOrNil(env map[string]string) *yaml.Node {
	data, err := yaml.Marshal(defaultConfig(env))
	if err != nil {
		return nil
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil
	}
	return unwrapDocument(&node)
}

// unwrapDocument returns the root mapping of a decoded YAML document node,
// or nil for an empty document.
func unwrapDocument(node *yaml.Node) *yaml.Node {
	if node == nil || node.Kind == 0 {
		return nil
	}
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil
		}
		return node.Content[0]
	}
	return node
}
