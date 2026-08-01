package runstate

import (
	"fmt"
	"os"
	"path/filepath"
)

// LockHeldError means a resume.lock already exists at Path — either another
// `oh-my-graph resume` for the same run id is genuinely in flight, or a prior
// one crashed without cleaning up after itself. It names the exact path so the
// user can tell the two apart and, if it's the latter, remove it (DESIGN.md,
// "resume.lock ... whose stale-lock failure mode has to be explained to the
// user with the exact path to delete").
type LockHeldError struct {
	Path string
}

func (e *LockHeldError) Error() string {
	return fmt.Sprintf(
		"another `resume` appears to be running for this run (lock held at %q); "+
			"if you're sure no other resume is running, delete it and retry: rm %q",
		e.Path, e.Path,
	)
}

// AcquireLock creates a resume.lock at path, guarding against two concurrent
// legs of the same run id double-running nodes — a `run`/`auto` first leg
// holds it for its whole duration and every `oh-my-graph resume` takes the
// same lock (DESIGN.md, "A resume.lock (O_EXCL, holding the pid) guards
// against two concurrent legs of the same run id"). O_EXCL makes the create
// atomic:
// exactly one caller among any racing to acquire the same path succeeds. The
// lock file's content is the holding process's pid — informational only
// (AcquireLock does not check whether that pid is still alive; a stale lock is
// a human decision, per LockHeldError).
//
// The returned release func removes the lock; the caller must call it exactly
// once, however the resume ends (success, failure, or another pause).
func AcquireLock(path string) (release func() error, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create run directory for lock %q: %w", path, err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, &LockHeldError{Path: path}
		}
		return nil, fmt.Errorf("create lock %q: %w", path, err)
	}

	if _, writeErr := fmt.Fprintf(f, "%d\n", os.Getpid()); writeErr != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("write lock %q: %w", path, writeErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		os.Remove(path)
		return nil, fmt.Errorf("close lock %q: %w", path, closeErr)
	}

	return func() error { return os.Remove(path) }, nil
}
