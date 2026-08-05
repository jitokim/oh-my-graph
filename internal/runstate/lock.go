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
//     so its flock says nothing and a different, weaker question has to be put
//     to it instead (see legacyLiveness).
//   - to AcquireLock, which semantics this lock file was written under
//     (see the "marked ⇒ flock, unmarked ⇒ legacy" branch there).
//
// In a file that HAS this line, everything after it — the pid — is explicitly
// informational and explicitly not a liveness test: the pid in a lock file was
// measured being recycled by an unrelated process (ADR 0015, "The pid in
// resume.lock is not a liveness test — measured"). The marker is what draws
// that boundary: a marked lock is decided by the flock alone and its pid line
// is never consulted, and only an unmarked one — where the flock cannot answer
// at all — is read for its pid, in the single direction pidGone documents.
// Never write this marker on a platform or path where the flock is not
// actually taken.
const LockFormatMarker = "oh-my-graph-lock 1"

// lockHeadBytes is how much of a lock file the marker/pid check reads. The
// body this package writes is two short lines; a longer head belongs to a file
// this package did not write, and reading a bounded prefix of it is enough to
// establish exactly that.
const lockHeadBytes = 128

// Liveness is the three-valued answer to "is a leg of this run still alive?",
// derived from the run's resume.lock (ADR 0015 §1). It is deliberately not a
// bool: a false *dead* authorises a second scheduler to re-run paid nodes over
// a live run, so every ambiguous case — a missing lock file, an unmarked one
// this package cannot read a pid out of, an unmarked one whose pid still names
// some process, a filesystem whose flock is not this flock, any probe error —
// folds into LivenessUnknown, whose meaning is "answer exactly as this tool did
// before ADR 0015" (an open leg reads as in flight).
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
	// LivenessFree means the leg that wrote this lock file is affirmatively
	// gone. Two facts can establish that, and nothing else may:
	//
	//   - a MARKED lock file (one this binary wrote) that nothing flocks — the
	//     kernel released it, however its holder died;
	//   - an UNMARKED, pre-flock lock file whose recorded pid names no process
	//     at all, which is the only question such a file can answer and the
	//     only direction it answers in (legacyLiveness, pidGone).
	//
	// This is the only value that may be composed into a verdict of ABANDONED,
	// and only beside a leg the event stream left open.
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
// that is the only semantics its writer knew, so the DECISION stands unchanged
// — the acquire is refused, a human decides, and the exact path to delete is
// printed. What changes is the evidence handed to that human. Since ProbeLock
// now reports such a run ABANDONED when its pid names no process
// (legacyLiveness), a bare "another `resume` appears to be running" would have
// the tool contradicting its own `runs list` on the adjacent line. So PIDGone
// records that finding, and it is set in one direction only: a pid that is gone
// is evidence worth showing, while a pid that is alive says nothing here (it
// may be a recycled stranger — ADR 0015, Context) and is left out of both the
// struct and the message rather than shown as a false reassurance.
type LockHeldError struct {
	Path string
	PID  int
	// Legacy marks a refusal over a pre-flock lock file: existence semantics,
	// human decides.
	Legacy bool
	// PIDGone is set only on the legacy arm, and only when PID names no
	// process at all. It never authorises anything on its own — it is the
	// evidence, not the decision.
	PIDGone bool
}

func (e *LockHeldError) Error() string {
	if e.Legacy {
		if e.PIDGone {
			return fmt.Sprintf(
				"this run's lock predates the flock format, so the lock itself cannot say whether a leg is running (lock: %q); "+
					"the pid it names, %d, no longer exists, so it is very likely stale — "+
					"if you're sure no other resume is running, delete it and retry: rm %q",
				e.Path, e.PID, e.Path,
			)
		}
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
		return nil, legacyLockHeldError(path, head)
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

// legacyLockHeldError is the refusal over a pre-flock lock file. The refusal
// itself never depends on the pid — an unmarked lock is refused whatever it
// says, because a live pre-flock leg holds no flock and only a human can rule
// one out. The pid only decides which evidence the message carries, and only
// when it is affirmatively gone.
func legacyLockHeldError(path string, head []byte) *LockHeldError {
	e := &LockHeldError{Path: path, Legacy: true}
	if pid, ok := legacyLockPID(head); ok && pidGone(pid) {
		e.PID, e.PIDGone = pid, true
	}
	return e
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
//   - any error at all.
//
// A lock file whose first line is not LockFormatMarker takes a different route
// entirely: its writer holds no flock, so the flock is silent about it and
// asking would be meaningless. legacyLiveness puts the only question such a
// file can answer to it instead, and folds every ambiguity in that answer into
// unknown as well.
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
	if err != nil {
		return LivenessUnknown
	}
	if !hasLockMarker(head) {
		return legacyLiveness(head)
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

// legacyLiveness answers for a lock file written before LockFormatMarker
// existed — a bare `<pid>\n`, all either specimen of the two zombie runs in
// ADR 0015's Context contains.
//
// Without it those runs are undiagnosable forever. The flock is silent about an
// unmarked file (its writer never took one), so folding "unmarked" into unknown
// means every run abandoned before the upgrade reads RUNNING for the rest of
// time — there is no later moment at which it becomes readable, because the
// only thing that could change is a pid, and the marker will never appear.
// That is a permanent wrong answer, and it is wrong in the direction the ADR
// spent itself avoiding everywhere else.
//
// The pid is the only signal such a file carries, and it is trustworthy in
// exactly one direction (pidGone documents why):
//
//	pid names no process → the leg that wrote this file has exited. Free.
//	pid names something  → holder or recycled stranger, indistinguishable.
//	                       Unknown, i.e. today's answer.
//	no readable pid      → nothing was said. Unknown.
//
// This does NOT weaken the asymmetry ADR 0015 is built on, on three counts.
// First, the conclusion rests on an affirmative kernel fact (ESRCH), not on the
// absence of one — the same shape as a succeeding LOCK_SH, not the shape of a
// missing file. Second, the failure the ADR measured is a false ALIVE produced
// by pid-alive reasoning, and pid-alive is never read as evidence here; the
// non-determinism it demonstrated (the same directory reading alive at 08:00
// and dead at 23:35) can only move this answer from free to unknown, never onto
// a live run. Third, the acquire path is deliberately not changed: an unmarked
// lock is still refused under legacy semantics with a human deciding, so no
// answer produced here can by itself start a second leg over a live one.
//
// The residual false-dead, stated because it is real and cannot be mechanised
// away: a pid is only meaningful inside the PID namespace of the process that
// wrote it. A pre-flock leg running inside a container, read by a binary
// outside it through a shared runs directory, could have its namespace-local
// pid read as gone while it is alive. Nothing in the pre-flock format records a
// namespace, so there is no check to make; what bounds the risk is that the
// case needs an OLD binary running right now beside a new one reading, that the
// filesystem gate above already refuses a runs root on a network mount, and
// that the human still stands on the acquire path.
func legacyLiveness(head []byte) Liveness {
	pid, ok := legacyLockPID(head)
	if !ok || !pidGone(pid) {
		return LivenessUnknown
	}
	return LivenessFree
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

// legacyLockPID reads the pid out of a pre-flock lock file, and refuses
// anything that is not exactly what acquireLegacyLock writes — one decimal
// line, positive, and nothing else in the file.
//
// The strictness is the point, because this pid is the one place in the design
// where a file's contents decide a run is gone. A second line means some other
// writer's file, not a pre-flock lock; a non-number means the same; a zero or
// negative pid means the same, and must additionally never reach kill(2), where
// those values name process GROUPS. An empty head is refused by the same rule
// and needs to be, because a marked lock file is momentarily empty while
// writeLockBody rewrites it under a held flock — reading that instant as an
// unreadable legacy file (unknown) is correct; reading it as anything else
// would call a live leg abandoned.
//
// The head is a bounded 128-byte prefix, so a longer file is refused by its
// unread remainder rather than judged on its first line: a run of digits past
// the buffer fails Atoi's range check, and anything else fails the one-line
// rule.
func legacyLockPID(head []byte) (int, bool) {
	line, rest, _ := bytes.Cut(head, []byte("\n"))
	if len(bytes.TrimSpace(rest)) != 0 {
		return 0, false
	}
	pid, err := strconv.Atoi(string(bytes.TrimSpace(line)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
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
