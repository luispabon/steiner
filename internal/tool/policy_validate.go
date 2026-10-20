package tool

// ValidateToolInput normalizes path-bearing tool arguments.
func (p PathPolicy) ValidateToolInput(toolName string, input map[string]any) (map[string]any, error) {
	normalized := CloneJSONMap(input)
	switch toolName {
	case "read", "glob", "grep", "ls":
		return p.validateReadOnlyToolInput(normalized)
	case "mutate":
		return p.validateMutateToolInput(normalized)
	case "bash":
		return p.validateBashToolInput(normalized)
	}
	return normalized, nil
}

func (p PathPolicy) previewToolInput(toolName string, input map[string]any) (ApprovalPreview, error) {
	normalized, err := p.ValidateToolInput(toolName, input)
	if err != nil {
		return ApprovalPreview{}, err
	}
	return buildApprovalPreview(toolName, normalized, p), nil
}

func (p PathPolicy) validateReadOnlyToolInput(input map[string]any) (map[string]any, error) {
	path := stringInput(input["path"])
	if path == "" {
		path = "."
		input["path"] = "."
	}
	resolved, err := p.ResolveReadPath(path)
	if err != nil {
		return nil, err
	}
	input["path"] = resolved
	return input, nil
}

func (p PathPolicy) validateMutateToolInput(input map[string]any) (map[string]any, error) {
	ops, ok := input["operations"].([]any)
	if !ok {
		return input, nil
	}

	normalizedOps := make([]any, 0, len(ops))
	for _, rawOp := range ops {
		normalizedOp, err := p.normalizeMutateOperation(rawOp)
		if err != nil {
			return nil, err
		}
		normalizedOps = append(normalizedOps, normalizedOp)
	}
	input["operations"] = normalizedOps
	return input, nil
}

func (p PathPolicy) normalizeMutateOperation(rawOp any) (any, error) {
	op, ok := rawOp.(map[string]any)
	if !ok {
		return rawOp, nil
	}
	nextOp := CloneJSONMap(op)
	for _, key := range []string{"path", "from", "to"} {
		resolved, err := p.resolveOptionalPath(nextOp[key], true)
		if err != nil {
			return nil, err
		}
		if resolved != "" {
			nextOp[key] = resolved
		}
	}
	return nextOp, nil
}

func (p PathPolicy) validateBashToolInput(input map[string]any) (map[string]any, error) {
	cwd, err := p.ResolveCWD(stringInput(input["cwd"]))
	if err != nil {
		return nil, err
	}
	if cwd != "" {
		input["cwd"] = cwd
	}
	return input, nil
}

func (p PathPolicy) resolveOptionalPath(value any, writable bool) (string, error) {
	path := stringInput(value)
	if path == "" {
		return "", nil
	}
	return p.ResolvePath(path, writable)
}

func stringInput(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}
