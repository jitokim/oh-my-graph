//go:build darwin || linux

package runstate

import (
	"errors"
	"os"
	"syscall"
)

// flockSupported is true on the platforms this project releases for
// (.goreleaser.yaml builds darwin and linux), where syscall.Flock is the BSD
// whole-file lock ADR 0015 §1 depends on. Every other GOOS takes the
// build-tagged stub, whose probe reports unknown and whose acquire keeps the
// pre-ADR-0015 behaviour — the tree still builds where the project does not
// ship.
const flockSupported = true

// flockExclusiveNB takes the leg's lock. It conflicts per open file
// description, not per process: a second fd on the same file in the SAME
// process is refused, which is exactly why this is flock(2) and not
// fcntl(F_SETLK) — the live view embedded in the run's own process probes the
// lock its own leg holds, and must be told "held".
func flockExclusiveNB(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// flockSharedNB is the read-time probe: it conflicts with a holder's exclusive
// lock (the question being asked) but not with another probe, so readers never
// flicker each other into a false "held".
func flockSharedNB(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
}

func flockUnlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// flockContended reports whether err is "somebody else holds it" rather than a
// failure to ask. Only this answer may be read as a live holder; anything else
// is an error, and an error is unknown.
func flockContended(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
