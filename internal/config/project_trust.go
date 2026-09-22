package config

import "errors"

// ProjectTrust says whether Load may apply the project config layer.
type ProjectTrust int

const (
	// ProjectTrustUntrusted (zero value) refuses to apply an existing project config.
	ProjectTrustUntrusted ProjectTrust = iota
	// ProjectTrustTrusted applies the project config layer.
	ProjectTrustTrusted
)

// ErrProjectUntrusted is returned by Load when a project config exists but
// LoadOptions.ProjectTrust is not ProjectTrustTrusted.
var ErrProjectUntrusted = errors.New("project config is not trusted")
