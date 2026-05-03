package tool

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

type ApprovalPreview struct {
	Tool    string              `json:"tool"`
	Mode    config.ApprovalMode `json:"mode"`
	WorkDir string              `json:"work_dir,omitempty"`
	Timeout time.Duration       `json:"timeout,omitempty"`
	Fields  []PreviewField      `json:"fields,omitempty"`
	Notes   []string            `json:"notes,omitempty"`
}

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
		cwd := stringInput(input["cwd"])
		if cwd != "" {
			preview.Fields = append(preview.Fields, PreviewField{Name: "cwd", Value: cwd})
		}
		if command := stringInput(input["command"]); command != "" {
			preview.Fields = append(preview.Fields, previewTextField("command", command, 160))
		}
	case "read", "write":
		if path := stringInput(input["path"]); path != "" {
			preview.Fields = append(preview.Fields, PreviewField{Name: "path", Value: path})
		}
		if toolName == "write" {
			if contents := stringInput(input["contents"]); contents != "" {
				preview.Fields = append(preview.Fields, previewTextField("contents", contents, 128))
			}
		}
	case "edit", "apply_patch":
		if path := stringInput(input["path"]); path != "" {
			preview.Fields = append(preview.Fields, PreviewField{Name: "path", Value: path})
		}
		if old := stringInput(input["old_string"]); old != "" {
			preview.Fields = append(preview.Fields, previewTextField("old_string", old, 128))
		}
	case "glob":
		if pattern := stringInput(input["pattern"]); pattern != "" {
			preview.Fields = append(preview.Fields, PreviewField{Name: "pattern", Value: pattern})
		}
	case "grep":
		if pattern := stringInput(input["pattern"]); pattern != "" {
			preview.Fields = append(preview.Fields, PreviewField{Name: "pattern", Value: pattern})
		}
		if path := stringInput(input["path"]); path != "" {
			preview.Fields = append(preview.Fields, PreviewField{Name: "path", Value: path})
		}
	case "ls":
		if path := stringInput(input["path"]); path != "" {
			preview.Fields = append(preview.Fields, PreviewField{Name: "path", Value: path})
		}
	default:
		keys := make([]string, 0, len(input))
		for key := range input {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			preview.Fields = append(preview.Fields, previewValueField(key, input[key]))
		}
	}

	return preview
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

func (p ApprovalPreview) Summary() string {
	parts := make([]string, 0, 4)
	if p.Tool != "" {
		parts = append(parts, "tool="+p.Tool)
	}
	if p.WorkDir != "" {
		parts = append(parts, "workdir="+p.WorkDir)
	}
	if p.Timeout > 0 {
		parts = append(parts, "timeout="+p.Timeout.String())
	}
	if len(p.Fields) > 0 {
		fieldParts := make([]string, 0, len(p.Fields))
		for _, field := range p.Fields {
			value := field.Value
			if field.Binary {
				value = "<binary>"
			} else if field.Truncated {
				value = value + " [truncated]"
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
