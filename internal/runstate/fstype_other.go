//go:build !darwin && !linux

package runstate

import "errors"

var errStatfsUnsupported = errors.New("filesystem type cannot be determined on this platform")

// isLocalFilesystem cannot answer off darwin and linux, so it refuses. Nothing
// reaches it today — ProbeLock returns LivenessUnknown on the flockSupported
// check before it gets here — and refusing rather than guessing keeps that
// true if the two checks ever come apart.
func isLocalFilesystem(string) (bool, error) {
	return false, errStatfsUnsupported
}
