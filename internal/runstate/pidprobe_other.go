//go:build !darwin && !linux

package runstate

// pidGone cannot answer where there is no syscall.Kill to ask, so it never
// claims a process is gone — the same one-directional default the rest of this
// package takes (see flock_other.go). Nothing reaches it today: ProbeLock
// returns LivenessUnknown on the flockSupported check long before the legacy
// arm, and the acquire path on these platforms is acquireLegacyLock, which
// refuses on O_EXCL without reading the file. Refusing rather than guessing
// keeps that true if those paths ever come apart.
func pidGone(int) bool { return false }
