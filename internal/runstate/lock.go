package runstate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// LockFileName is the run directory's lock file, named here because this
// package owns it: it is the file AcquireLock takes and ProbeLock reads, and
// since ADR 0015 §3 promoted it out of the internal set it is contract surface
// (docs/RUN-FEED.md, "Liveness") rather than a detail each caller may spell
// for itself.
const LockFileName = "resume.lock"

// LockFormatMarker is the first line of every resume.lock this binary writes,
// and the only promise the file's contents carry (ADR 0015 §3). It says two
// things at once, and both are load-bearing:
//
//   - to a READER, that the file was written by a binary that takes an
//     exclusive flock(2) on it for the leg's whole duration — so a *free* lock
//     beside an open leg means the writer is gone. A file without this line
//     was written by a pre-flock binary whose live leg holds no flock at all,
//     which is indistinguishable from a released one: unknown, never "free".
//   - to AcquireLock, which semantics this lock file was written under
//     (see the "marked ⇒ flock, unmarked ⇒ legacy" branch there).
//
// Everything after this line — the pid — is explicitly informational and
// explicitly not a liveness test: the pid in a lock file was measured being
// recycled by an unrelated process (ADR 0015, "The pid in resume.lock is not a
// liveness test — measured"). Never write this marker on a platform or path
// where the flock is not actually taken.
const LockFormatMarker = "oh-my-graph-lock 1"

// lockHeadBytes is how much of a lock file the marker/pid check reads. The
// body this package writes is two short lines; a longer head belongs to a file
// this package did not write, and reading a bounded prefix of it is enough to
// establish exactly that.
const lockHeadBytes = 128

// Liveness is the three-valued answer to "is a leg of this run still alive?",
// derived from the run's resume.lock (ADR 0015 §1). It is deliberately not a
// bool: a false *dead* authorises a second scheduler to re-run paid nodes over
// a live run, so every ambiguous case — a missing lock file, an unmarked one,
// a filesystem whose flock is not this flock, any probe error — folds into
// LivenessUnknown, whose meaning is "answer exactly as this tool did before
// ADR 0015" (an open leg reads as in flight).
//
// The zero value is LivenessUnknown, so a Liveness nobody set can never claim
// a run is dead.
type Liveness int

const (
	// LivenessUnknown means the lock could not answer. Callers must fall back
	// to the pre-ADR-0015 reading of the event stream alone.
	LivenessUnknown Liveness = iota
	// LivenessHeld means a process holds the run's lock right now: a leg is
	// alive. Immune to pid recycling — the kernel releases the flock when the
	// holder dies, however it dies, and there is no name to recycle.
	LivenessHeld
	// LivenessFree means the lock file is one this binary wrote (it carries
	// LockFormatMarker) and nothing holds it: the leg that wrote it is gone.
	// This is the only value that may be composed into a verdict of
	// ABANDONED, and only beside a leg the event stream left open.
	LivenessFree
)

func (l Liveness) String() string {
	switch l {
	case LivenessHeld:
		return "held"
	case LivenessFree:
		return "free"
	default:
		return "unknown"
	}
}

// LockHeldError means AcquireLock refused: another leg of this run is in
// flight, or a lock file written under pre-flock semantics is in the way. The
// two cases get different words because they need different actions, and the
// Legacy field says which one this is.
//
// On the flock arm (Legacy false) the refusal is a kernel fact — some process
// holds the lock right now — and the advice is to wait for it or stop it.
// PID, when non-zero, is the pid the holder wrote: a LABEL for a live process,
// never a target to kill and never the thing that established liveness. It is
// trustworthy here only because the flock already proved the holder alive; the
// inference in the other direction (a pid means a live process) is the one the
// ADR's measurements refute.
//
// Deliberately absent from the flock arm: the old "delete it and retry: rm
// <path>" advice. Under flock semantics unlinking the file does not release
// the live holder's lock — it keeps holding it on the now-unlinked inode —
// while the next leg creates a fresh file and takes an uncontended flock on
// the new inode: two schedulers, one run, both spending (ADR 0015 §4).
//
// On the legacy arm (Legacy true) the file's existence IS the lock, because
// that is the only semantics its writer knew, so the old message stands
// unchanged — a human decides, and the exact path to delete is the useful
// thing to print.
type LockHeldError struct {
	Path   string
	PID    int
	Legacy bool
}

func (e *LockHeldError) Error() string {
	if e.Legacy {
		return fmt.Sprintf(
			"another `resume` appears to be running for this run (lock held at %q); "+
				"if you're sure no other resume is running, delete it and retry: rm %q",
			e.Path, e.Path,
		)
	}
	if e.PID > 0 {
		return fmt.Sprintf(
			"a leg of this run is in flight (started by pid %d); wait for it, or stop it (lock: %q)",
			e.PID, e.Path,
		)
	}
	return fmt.Sprintf(
		"a leg of this run is in flight; wait for it, or stop it (lock: %q)",
		e.Path,
	)
}

// AcquireLock takes the run's resume.lock at path, guarding against two
// concurrent legs of the same run id double-running (double-billing) nodes: a
// `run`/`auto` first leg holds it for its whole duration and every
// `oh-my-graph resume` takes the same lock.
//
// The lock is the kernel's exclusive flock(2) on the file, not the file's
// existence (ADR 0015 §1). That is what lets a reader ask the same file a
// second question — "is that leg still alive?" — and get an answer immune to
// pid recycling: see ProbeLock. Two consequences shape this function:
//
//   - The file is opened O_CREATE|O_RDWR, never O_EXCL, and it is truncated
//     and rewritten ONLY once the flock is held. An O_TRUNC in the open flags
//     would run before the lock is taken, so a caller that then lost the race
//     would already have blanked the live holder's file.
//   - The marker selects the semantics. A lock file whose first line is not
//     LockFormatMarker was written by a pre-ADR-0015 binary, whose live leg
//     holds no flock at all; taking an uncontended flock against it would
//     double-run the run. So an unmarked lock file is refused under the legacy
//     rule its writer knew (existence is the lock, a human decides, the
//     message names the path to delete). That arm is self-expiring: once such
//     a lock is cleared, the next acquire writes a marked one and the run is
//     on flock semantics forever.
//
// The returned release func unlocks and closes. It deliberately does NOT
// unlink: acquiring is open-then-flock, and an unlink between those two steps
// lets one caller hold the flock on an orphaned inode while another takes it
// on a freshly created one — two schedulers, one run (ADR 0015 §1, "Release
// must not unlink"). A resume.lock is therefore a permanent, inert resident of
// every run directory, and nothing anywhere may read its existence as a state.
// The caller must call release exactly once, however the leg ends.
//
// On a platform without flock(2) this falls back to the pre-ADR-0015 behaviour
// in full — O_EXCL, pid, unlink on release, and no marker, since the marker is
// a promise that a flock is being held.
func AcquireLock(path string) (release func() error, err error) {
	l, err := acquireLock(path)
	if err != nil {
		return nil, err
	}
	return l.release, nil
}

// heldLock is an acquired lock and the way to give it back. It exists so the
// flock is tied to the *open file description* that took it — closing that one
// file is what releases it — rather than to the path, which no longer
// identifies the lock.
type heldLock struct {
	path string
	file *os.File
	// legacy marks a lock taken under pre-flock semantics (the platform has no
	// flock(2)): its file's existence is the lock, so its release must unlink.
	legacy bool
}

func (l *heldLock) release() error {
	if l.legacy {
		if err := l.file.Close(); err != nil {
			return fmt.Errorf("close lock %q: %w", l.path, err)
		}
		return os.Remove(l.path)
	}
	unlockErr := flockUnlock(l.file)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("unlock %q: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close lock %q: %w", l.path, closeErr)
	}
	return nil
}

func acquireLock(path string) (*heldLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create run directory for lock %q: %w", path, err)
	}
	if !flockSupported {
		return acquireLegacyLock(path)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %q: %w", path, err)
	}

	// Read before locking, so a pre-flock binary's lock file is refused rather
	// than flocked out from under its live leg. A file this binary is midway
	// through rewriting reads as empty here, which takes the same path as a
	// file we just created — harmless, because the flock below, not this read,
	// is what decides.
	head, err := lockHead(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("read lock %q: %w", path, err)
	}
	if len(head) > 0 && !hasLockMarker(head) {
		f.Close()
		return nil, &LockHeldError{Path: path, Legacy: true}
	}

	if lockErr := flockExclusiveNB(f); lockErr != nil {
		// The holder wrote its pid under the lock we just failed to take, so
		// it is a live process's pid: worth naming, never worth trusting on
		// its own.
		held, _ := lockHead(f)
		f.Close()
		if flockContended(lockErr) {
			return nil, &LockHeldError{Path: path, PID: lockPID(held)}
		}
		return nil, fmt.Errorf("lock %q: %w", path, lockErr)
	}

	if writeErr := writeLockBody(f); writeErr != nil {
		flockUnlock(f)
		f.Close()
		return nil, fmt.Errorf("write lock %q: %w", path, writeErr)
	}
	return &heldLock{path: path, file: f}, nil
}

// acquireLegacyLock is the pre-ADR-0015 lock, kept verbatim for platforms with
// no flock(2): O_EXCL makes the create atomic, the content is the pid, and
// release unlinks. It writes no marker, because a reader must never conclude
// "free, therefore abandoned" from a file whose writer holds no flock.
func acquireLegacyLock(path string) (*heldLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, &LockHeldError{Path: path, Legacy: true}
		}
		return nil, fmt.Errorf("create lock %q: %w", path, err)
	}
	if _, writeErr := fmt.Fprintf(f, "%d\n", os.Getpid()); writeErr != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("write lock %q: %w", path, writeErr)
	}
	return &heldLock{path: path, file: f, legacy: true}, nil
}

// ProbeLock answers whether a leg still holds the run's lock at path, without
// creating, writing or removing anything: it opens the file read-only, takes a
// SHARED (LOCK_SH) lock, and unlocks immediately (ADR 0015 §1, §3). A shared
// probe conflicts with the holder's exclusive lock — which is the whole
// question — but not with other probes, so no two readers can flicker each
// other into a false "held", and a poller never blocks a starting leg for
// longer than its own two syscalls.
//
// Every ambiguity is LivenessUnknown, because a false *dead* is the dangerous
// direction:
//
//   - no flock(2) on this platform;
//   - the run directory is not on a known-local filesystem. This is a gate,
//     not a contingency: on linux, flock() over NFS is emulated as whole-file
//     POSIX record locks, which are per-process (so the live view embedded in
//     the very process holding the lock would be granted it and declare its
//     own run abandoned) and are dropped when ANY fd on the file is closed by
//     the process (so this probe's own Close would release the run's real
//     lock). Neither returns an error, so the file must not even be OPENED
//     there;
//   - the file is missing — a directory predating this change, or a lock a
//     human deleted;
//   - the first line is not LockFormatMarker — a pre-flock binary's lock,
//     whose live leg holds no flock;
//   - any error at all.
//
// A run is declared abandoned only on an affirmative LivenessFree beside a leg
// the stream left open. Nothing is ever abandoned because a probe failed.
func ProbeLock(path string) Liveness {
	if !flockSupported {
		return LivenessUnknown
	}
	if local, err := isLocalFilesystem(filepath.Dir(path)); err != nil || !local {
		return LivenessUnknown
	}
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return LivenessUnknown
	}
	defer f.Close()

	head, err := lockHead(f)
	if err != nil || !hasLockMarker(head) {
		return LivenessUnknown
	}

	switch lockErr := flockSharedNB(f); {
	case lockErr == nil:
		flockUnlock(f)
		return LivenessFree
	case flockContended(lockErr):
		return LivenessHeld
	default:
		return LivenessUnknown
	}
}

// writeLockBody rewrites the lock file as marker line + pid line. It truncates
// first — with O_EXCL gone a lock file is reopened and reused, so a shorter
// pid than the previous holder's would otherwise leave that holder's digits
// trailing and LockHeldError would quote a stale pid. Both lines go out in one
// Write, so the window in which a concurrent reader sees a marked-but-pidless
// file does not exist.
func writeLockBody(f *os.File) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err := f.Write([]byte(fmt.Sprintf("%s\n%d\n", LockFormatMarker, os.Getpid())))
	return err
}

// lockHead reads a bounded prefix of the lock file at absolute offset 0, so it
// never disturbs the file offset of an fd the caller is also writing through.
func lockHead(f *os.File) ([]byte, error) {
	buf := make([]byte, lockHeadBytes)
	n, err := f.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:n], nil
}

func hasLockMarker(head []byte) bool {
	line, _, _ := bytes.Cut(head, []byte("\n"))
	return string(bytes.TrimRight(line, "\r")) == LockFormatMarker
}

// lockPID reads the informational pid line of a marked lock file, or 0 if
// there is not one to read. Only ever used as a label on an error message.
func lockPID(head []byte) int {
	_, rest, ok := bytes.Cut(head, []byte("\n"))
	if !ok {
		return 0
	}
	line, _, _ := bytes.Cut(rest, []byte("\n"))
	pid, err := strconv.Atoi(string(bytes.TrimSpace(line)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}
