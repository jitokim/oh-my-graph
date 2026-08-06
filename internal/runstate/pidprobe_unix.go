//go:build darwin || linux

package runstate

import (
	"errors"
	"syscall"
)

// pidGone reports the one thing a bare pid can be trusted to say: that NO
// process bears it right now. It exists for pre-flock lock files only — a file
// with LockFormatMarker is decided by the flock and its pid is never consulted
// (ADR 0015 §1) — and it is deliberately one-directional.
//
// The asymmetry is the whole justification. ADR 0015's measured refutation of
// pid probes is a refutation of pid-ALIVE reasoning: pid 80834 was recycled by
// an unrelated zsh, so "a process bears this pid" did not mean "the holder is
// alive", and the same probe answered differently in the morning and at night.
// pid-DEAD carries no such failure: a running process always bears its pid, so
// ESRCH means the process that wrote this number has exited. There is no
// recycling story that makes a live holder invisible, because recycling only
// ever ADDS a process under the name.
//
// So: only ESRCH is evidence, and every other outcome is "not gone".
//
//   - nil — a process bears it. It may be the holder, it may be a recycled
//     stranger; inconclusive, and inconclusive is not evidence.
//   - EPERM — a process bears it and belongs to another user. Existence is what
//     was asked, and the answer is yes.
//   - a zombie awaiting reap — kill(2) succeeds against it, so this reports
//     "not gone" for a process that has in fact exited. Imprecise in the safe
//     direction: the run keeps reading as it did before ADR 0015.
//   - any other error — unclassified, therefore not evidence.
//
// pid <= 0 is refused before the syscall rather than passed to it. kill(0, sig)
// addresses the CALLER's whole process group and kill(-n, sig) addresses group
// n; both would succeed, and the first would succeed by finding the reader
// itself — a malformed lock file must not be able to make this function answer
// a question nobody asked.
//
// Signal 0 sends nothing: it runs kill(2)'s existence and permission checks and
// returns. This is a syscall on an integer, not a process spawn, so the four
// exec seams (ADR 0002, 0005, 0006) are untouched and no fifth-seam ADR is
// owed — which is the constraint that rules out every `ps`-shaped alternative.
func pidGone(pid int) bool {
	if pid <= 0 {
		return false
	}
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
