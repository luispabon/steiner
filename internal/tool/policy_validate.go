package tool

import "errors"

// maxCollectedOutsidePaths bounds how many distinct outside paths one approval
// carries. The total distinct count is reported separately, so a large batch
// cannot inflate the prompt or its event payload.
const maxCollectedOutsidePaths = 20

// ValidateToolInput normalizes path-bearing tool arguments.
func (p PathPolicy) ValidateToolInput(toolName string, input map[string]any) (map[string]any, error) {
	normalized := CloneJSONMap(input)
	switch toolName {
	case "read", "glob", "grep":
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

	// A batch is authorized or denied as a whole, so every operation is
	// checked before the call can run. Outside-root paths are gathered across
	// the batch, while any other violation still fails the call closed without
	// a prompt.
	outside := newOutsidePaths()
	normalizedOps := make([]any, 0, len(ops))
	for _, rawOp := range ops {
		normalizedOp, err := p.normalizeMutateOperation(rawOp, outside)
		if err != nil {
			return nil, err
		}
		normalizedOps = append(normalizedOps, normalizedOp)
	}
	if err := outside.err(p); err != nil {
		return nil, err
	}
	input["operations"] = normalizedOps
	return input, nil
}

func (p PathPolicy) normalizeMutateOperation(rawOp any, outside *outsidePaths) (any, error) {
	op, ok := rawOp.(map[string]any)
	if !ok {
		return rawOp, nil
	}
	nextOp := CloneJSONMap(op)
	for _, key := range []string{"path", "from", "to"} {
		resolved, err := p.resolveOptionalPath(nextOp[key], true)
		if err != nil {
			var policyErr *PathPolicyError
			if errors.As(err, &policyErr) && policyErr.Promptable {
				outside.add(policyErr.Path)
				continue
			}
			return nil, err
		}
		if resolved != "" {
			nextOp[key] = resolved
		}
	}
	return nextOp, nil
}

// outsidePaths accumulates the distinct promptable outside paths of one call in
// encounter order. It keeps at most maxCollectedOutsidePaths entries but counts
// every distinct path it sees.
type outsidePaths struct {
	paths []string
	seen  map[string]struct{}
	total int
}

func newOutsidePaths() *outsidePaths {
	return &outsidePaths{seen: make(map[string]struct{})}
}

func (o *outsidePaths) add(path string) {
	if path == "" {
		return
	}
	if _, ok := o.seen[path]; ok {
		return
	}
	o.seen[path] = struct{}{}
	o.total++
	if len(o.paths) < maxCollectedOutsidePaths {
		o.paths = append(o.paths, path)
	}
}

// err reports the collected outside paths as one promptable violation, or nil
// when the call stayed inside the project root.
func (o *outsidePaths) err(policy PathPolicy) error {
	if o.total == 0 {
		return nil
	}
	return policy.outsideRootError(o.paths, o.total)
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
