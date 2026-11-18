package oneshot

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ResolutionKind identifies how an operator resolves a closeout whose outcome
// is unknown.
type ResolutionKind string

const (
	// ResolutionRecordURL records an external request the operator found.
	ResolutionRecordURL ResolutionKind = "url"
	// ResolutionRetry authorises one more closeout attempt after the operator
	// confirmed that no external request exists.
	ResolutionRetry ResolutionKind = "retry"
)

// Resolution is one explicit manual closeout resolution.
type Resolution struct {
	Kind ResolutionKind
	URL  string
}

// ResolveCloseout records an explicit resolution for a run whose closeout
// outcome is unknown. It takes the run's exclusive lock. Recording a URL is
// also accepted for an operator-authorised retry, because the request the retry
// would open may already exist; every other state is rejected.
func ResolveCloseout(ctx context.Context, deps Dependencies, resolution Resolution) (manifest Manifest, err error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	closeoutURL, err := validateResolution(resolution)
	if err != nil {
		return Manifest{}, err
	}

	store := resolutionStore(deps)
	loaded, err := store.Read()
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve closeout: load manifest: %w", err)
	}
	identity, err := resolutionIdentity(deps, loaded)
	if err != nil {
		return Manifest{}, err
	}
	lock, err := resolutionLock(deps, identity)
	if err != nil {
		return Manifest{}, err
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
	}()

	// Re-read under the lock: another process may have resolved the run first.
	manifest, err = store.Read()
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve closeout: reload manifest: %w", err)
	}
	// A resolution records the outcome of a closeout that already ran. A run
	// that has not finished its phases, or has not written its report, reaches
	// its own finalization on resume, so resolving it here could stamp a result
	// that a later closeout duplicates or overwrites.
	if phase, incomplete := firstIncompletePhase(manifest); incomplete {
		return manifest, fmt.Errorf("resolve closeout: run %s still has incomplete phases (next: %s); finish it with --resume first", manifest.RunID, phase)
	}
	if reportPending(manifest) {
		return manifest, fmt.Errorf("resolve closeout: run %s has no written final report; finish it with --resume first", manifest.RunID)
	}
	// A URL supersedes an authorised retry: the operator found the request the
	// retry would have opened. A retry resolution still requires an unknown
	// outcome, so it cannot be repeated on a run that already authorised one.
	if !closeoutOutcomeUnknown(manifest) && (resolution.Kind != ResolutionRecordURL || !closeoutRetryable(manifest)) {
		return manifest, fmt.Errorf("resolve closeout: run %s closeout state %q is not unknown, nothing to resolve", manifest.RunID, manifest.CloseoutState)
	}

	manifest.FinalizationVersion = finalizationVersion
	manifest.CloseoutIntent = ""
	manifest.CloseoutNote = ""
	if resolution.Kind == ResolutionRecordURL {
		manifest.CloseoutURL = closeoutURL
		manifest.CloseoutState = closeoutStateCreated
	} else {
		manifest.CloseoutState = closeoutStateRetryAuthorized
	}
	if err := store.Write(manifest); err != nil {
		return manifest, fmt.Errorf("persist closeout resolution: %w", err)
	}
	return manifest, nil
}

// resolutionStore returns the manifest store a resolution writes to, mirroring
// Resume's defaults.
func resolutionStore(deps Dependencies) *ManifestStore {
	if deps.ManifestStore != nil {
		return deps.ManifestStore
	}
	return NewManifestStore(deps.Identity.ManifestPath(deps.ProjectRoot))
}

// resolutionIdentity returns the identity a resolution targets: the configured
// one when it names the run, otherwise the manifest's. It rejects a manifest
// without a run id and an identity that names a different run.
func resolutionIdentity(deps Dependencies, manifest Manifest) (RunIdentity, error) {
	if strings.TrimSpace(manifest.RunID) == "" {
		return RunIdentity{}, errors.New("resolve closeout: manifest run id is required")
	}
	configured := strings.TrimSpace(deps.Identity.ID)
	if configured != "" && manifest.RunID != configured {
		return RunIdentity{}, fmt.Errorf("resolve closeout: manifest run id %q does not match requested run %q", manifest.RunID, configured)
	}
	if configured == "" {
		return RunIdentity{ID: manifest.RunID, Slug: manifest.Slug}, nil
	}
	return deps.Identity, nil
}

// resolutionLock acquires the run's exclusive lock so a resolution cannot race
// an active run or a second operator.
func resolutionLock(deps Dependencies, identity RunIdentity) (*RunLock, error) {
	lockFactory := deps.RunLockFactory
	if lockFactory == nil {
		lockFactory = AcquireRunLock
	}
	return lockFactory(deps.ProjectRoot, identity)
}

// validateResolution rejects incomplete resolutions and returns the URL to
// record, trimmed. It never infers that an external request does not exist.
func validateResolution(resolution Resolution) (string, error) {
	trimmed := strings.TrimSpace(resolution.URL)
	switch resolution.Kind {
	case ResolutionRecordURL:
		if trimmed == "" {
			return "", errors.New("resolve closeout: a URL is required to record an external request")
		}
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return "", fmt.Errorf("resolve closeout: parse URL: %w", err)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", fmt.Errorf("resolve closeout: URL scheme %q is not http or https", parsed.Scheme)
		}
		if parsed.Host == "" {
			return "", errors.New("resolve closeout: URL host is required")
		}
		return trimmed, nil
	case ResolutionRetry:
		if trimmed != "" {
			return "", errors.New("resolve closeout: a retry resolution takes no URL")
		}
		return "", nil
	default:
		return "", fmt.Errorf("resolve closeout: unknown resolution kind %q", resolution.Kind)
	}
}
