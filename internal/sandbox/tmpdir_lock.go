package sandbox

import "errors"

// errTmpDirLockBusy reports that another process holds the liveness lock on a
// sandbox tmp directory, which means the directory belongs to a live runtime.
var errTmpDirLockBusy = errors.New("sandbox tmp dir is locked by a live process")

// errTmpDirLockUnsupported reports that the platform offers no advisory
// directory lock, so tmp directory liveness cannot be determined.
var errTmpDirLockUnsupported = errors.New("sandbox tmp dir locking is unsupported on this platform")

// tmpDirLockAcquire acquires the lifetime liveness lock on a sandbox tmp
// directory. Tests replace it to simulate lock failures.
var tmpDirLockAcquire = acquireTmpDirLock
