package tool

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

// ApprovalPreview captures the fields shown before an approval decision.
type ApprovalPreview struct {
	Tool    string              `json:"tool"`
	Mode    config.ApprovalMode `json:"mode"`
	WorkDir string              `json:"work_dir,omitempty"`
	Timeout time.Duration       `json:"timeout,omitempty"`
	Fields  []PreviewField      `json:"fields,omitempty"`
	Notes   []string            `json:"notes,omitempty"`
}

// PreviewField captures a single field displayed in an approval preview.
type PreviewField struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Truncated bool   `json:"truncated,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
}

func buildApprovalPreview(toolName string, input map[string]any, policy PathPolicy) ApprovalPreview {
	preview := ApprovalPreview{
		Tool:   toolName,
		Fields: make([]PreviewField, 0, len(input)),
	}
	if policy.root != "" {
		preview.WorkDir = policy.root
	}

	switch toolName {
	case "bash":
		return buildBashApprovalPreview(preview, input)
	case "read", "write":
		return buildReadWriteApprovalPreview(preview, toolName, input)
	case "edit":
		return buildEditApprovalPreview(preview, input)
	case "apply_patch":
		return buildPatchApprovalPreview(preview, input)
	case "glob":
		return buildGlobApprovalPreview(preview, input)
	case "grep":
		return buildGrepApprovalPreview(preview, input)
	case "ls":
		return buildLSApprovalPreview(preview, input)
	default:
		return buildGenericApprovalPreview(preview, input)
	}
}

func buildBashApprovalPreview(preview ApprovalPreview, input map[string]any) ApprovalPreview {
	cwd := stringInput(input["cwd"])
	if cwd != "" {
		preview.Fields = append(preview.Fields, PreviewField{Name: "cwd", Value: cwd})
	}
	if command := stringInput(input["command"]); command != "" {
		preview.Fields = append(preview.Fields, previewTextField("command", command, 160))
	}
	return preview
}

func buildReadWriteApprovalPreview(preview ApprovalPreview, toolName string, input map[string]any) ApprovalPreview {
	if path := stringInput(input["path"]); path != "" {
		preview.Fields = append(preview.Fields, PreviewField{Name: "path", Value: path})
	}
	if toolName == "write" {
		if contents := stringInput(input["contents"]); contents != "" {
			preview.Fields = append(preview.Fields, previewTextField("contents", contents, 128))
		}
	}
	return preview
}

func buildEditApprovalPreview(preview ApprovalPreview, input map[string]any) ApprovalPreview {
	if path := stringInput(input["path"]); path != "" {
		preview.Fields = append(preview.Fields, PreviewField{Name: "path", Value: path})
	}
	if old := stringInput(input["old_string"]); old != "" {
		preview.Fields = append(preview.Fields, previewTextField("old_string", old, 128))
	}
	return preview
}

func buildPatchApprovalPreview(preview ApprovalPreview, input map[string]any) ApprovalPreview {
	if patch := stringInput(input["patch"]); patch != "" {
		preview.Fields = append(preview.Fields, previewTextField("patch", patch, 160))
	}
	return preview
}

func buildGlobApprovalPreview(preview ApprovalPreview, input map[string]any) ApprovalPreview {
	if pattern := stringInput(input["pattern"]); pattern != "" {
		preview.Fields = append(preview.Fields, PreviewField{Name: "pattern", Value: pattern})
	}
	return preview
}

func buildGrepApprovalPreview(preview ApprovalPreview, input map[string]any) ApprovalPreview {
	if pattern := stringInput(input["pattern"]); pattern != "" {
		preview.Fields = append(preview.Fields, PreviewField{Name: "pattern", Value: pattern})
	}
	if path := stringInput(input["path"]); path != "" {
		preview.Fields = append(preview.Fields, PreviewField{Name: "path", Value: path})
	}
	return preview
}

func buildLSApprovalPreview(preview ApprovalPreview, input map[string]any) ApprovalPreview {
	if path := stringInput(input["path"]); path != "" {
		preview.Fields = append(preview.Fields, PreviewField{Name: "path", Value: path})
	}
	return preview
}

func buildGenericApprovalPreview(preview ApprovalPreview, input map[string]any) ApprovalPreview {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		preview.Fields = append(preview.Fields, previewValueField(key, input[key]))
	}
	return preview
}

func relDisplayPath(workDir, path string) string {
	workDir = strings.TrimSpace(workDir)
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if workDir == "" {
		return filepath.Base(path)
	}
	workDir = filepath.Clean(workDir)
	if path == workDir {
		return "."
	}
	if rel, err := filepath.Rel(workDir, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return rel
	}
	if filepath.IsAbs(path) {
		return filepath.Base(path)
	}
	return path
}

func previewTextField(name, value string, limit int) PreviewField {
	if limit < 0 {
		limit = 0
	}
	if len(value) <= limit {
		return PreviewField{Name: name, Value: value}
	}
	return PreviewField{
		Name:      name,
		Value:     value[:limit],
		Truncated: true,
	}
}

func summarizePreviewCommand(command, workDir string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	command = strings.Join(strings.Fields(command), " ")
	if strings.HasPrefix(strings.ToLower(command), "cd ") {
		for _, sep := range []string{" && ", " ; ", " || "} {
			if idx := strings.Index(command, sep); idx >= 0 {
				command = strings.TrimSpace(command[idx+len(sep):])
				break
			}
		}
	}
	command = redactPreviewRoot(command, workDir)
	if len(command) <= 96 {
		return command
	}
	return command[:96] + "..."
}

func redactPreviewRoot(text, root string) string {
	text = strings.TrimSpace(text)
	root = strings.TrimSpace(root)
	if text == "" || root == "" {
		return text
	}
	root = filepath.Clean(root)
	if text == root {
		return "."
	}
	text = strings.ReplaceAll(text, root+string(filepath.Separator), "")
	return strings.ReplaceAll(text, root, "")
}

func previewValueField(name string, value any) PreviewField {
	switch v := value.(type) {
	case string:
		return previewTextField(name, v, 128)
	case StreamCapture:
		return PreviewField{
			Name:      name,
			Value:     v.Summary(),
			Truncated: v.Truncated,
			Binary:    v.Binary,
		}
	case ExecutionMetadata:
		return PreviewField{
			Name:  name,
			Value: v.Summary(),
		}
	case fmt.Stringer:
		return previewTextField(name, v.String(), 128)
	default:
		return PreviewField{Name: name, Value: fmt.Sprint(value)}
	}
}

// Summary returns a compact single-line description of the preview.
func (p ApprovalPreview) Summary() string {
	parts := make([]string, 0, 4)
	if p.Tool != "" {
		parts = append(parts, "tool="+p.Tool)
	}
	if p.WorkDir != "" {
		parts = append(parts, "workdir="+relDisplayPath(p.WorkDir, p.WorkDir))
	}
	if p.Timeout > 0 {
		parts = append(parts, "timeout="+p.Timeout.String())
	}
	if len(p.Fields) > 0 {
		fieldParts := make([]string, 0, len(p.Fields))
		for _, field := range p.Fields {
			value := field.Value
			switch {
			case field.Binary:
				value = "<binary>"
			case field.Truncated:
				value += " [truncated]"
			default:
				switch field.Name {
				case "cwd", "path":
					value = relDisplayPath(p.WorkDir, value)
				case "command":
					value = summarizePreviewCommand(value, p.WorkDir)
				}
			}
			fieldParts = append(fieldParts, field.Name+"="+value)
		}
		parts = append(parts, "fields="+strings.Join(fieldParts, ","))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}
