//go:build !darwin && !linux

package runstate

import (
	"errors"
	"os"
)

// flockSupported is false everywhere this project does not release to —
// windows above all, following the procgroup_{unix,windows}.go pattern. The
// consequences are deliberate and one-directional: ProbeLock returns
// LivenessUnknown, so nothing is ever declared abandoned here, and AcquireLock
// falls back to the pre-ADR-0015 O_EXCL lock, which is the behaviour these
// platforms have today. Nothing regresses; the new precision is simply absent.
const flockSupported = false

var errFlockUnsupported = errors.New("flock(2) is not available on this platform")

func flockExclusiveNB(*os.File) error { return errFlockUnsupported }

func flockSharedNB(*os.File) error { return errFlockUnsupported }

func flockUnlock(*os.File) error { return errFlockUnsupported }

func flockContended(error) bool { return false }
