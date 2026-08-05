//go:build darwin || linux

package runstate

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// This file pins the three platform properties ADR 0015's decision leans on
// and could not measure everywhere. Every measurement in that ADR was taken on
// darwin 22.6.0/APFS by hand with a throwaway probe; CI is ubuntu-only, so
// these tests are the linux half of the evidence — "an implementation gate,
// not a hope" (ADR 0015, "What could not be determined" #1). They are tagged
// darwin || linux, the two platforms .goreleaser.yaml ships, so the maintainer
// runs them locally on the platform CI will never cover.

// TestFlock_SharedProbeConflictsWithHeldExclusive is property three, and the
// one the ADR flags as newly load-bearing: the read-time probe takes LOCK_SH,
// so LOCK_SH MUST conflict with a held LOCK_EX. If it did not, every probe
// would succeed and every open leg would read as abandoned — a false dead on
// every live run, the worst outcome this design has.
func TestFlock_SharedProbeConflictsWithHeldExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.lock")
	held, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}

	probe := openForProbe(t, path)
	if err := flockSharedNB(probe); !flockContended(err) {
		t.Fatalf("LOCK_SH against a held LOCK_EX = %v, want a contended error", err)
	}

	if err := held.release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := flockSharedNB(probe); err != nil {
		t.Fatalf("LOCK_SH against a free lock = %v, want success", err)
	}
	if err := flockUnlock(probe); err != nil {
		t.Fatalf("unlock the probe: %v", err)
	}
}

// TestFlock_TwoDescriptorsInOneProcessConflict is property two, and the reason
// the design is flock(2) rather than fcntl(F_SETLK) record locks. The live
// view a `run` starts is served IN the very process that holds the lock, so if
// a second fd in one process were granted the lock, that view would declare
// its own live run abandoned. flock conflicts per open file description, which
// is exactly what makes it safe here.
func TestFlock_TwoDescriptorsInOneProcessConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.lock")
	held, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}
	defer held.release()

	second, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open a second descriptor: %v", err)
	}
	defer second.Close()

	if err := flockExclusiveNB(second); !flockContended(err) {
		t.Fatalf("LOCK_EX from a second fd in the same process = %v, want a contended error", err)
	}
	// And the probe, which is the case this actually protects.
	if got := ProbeLock(path); got != LivenessHeld {
		t.Fatalf("ProbeLock from the holding process = %v, want %v", got, LivenessHeld)
	}
}

// TestFlock_LockDescriptorIsCloseOnExec is property one. The lock must not
// reach a node's `claude` child, or a run whose engine died would keep reading
// as in flight for as long as its orphaned child lived — a false ALIVE that
// never clears, and the abandoned-run derivation would never fire at all. Go
// opens files O_CLOEXEC; this asserts that on the lock's own descriptor rather
// than trusting it, and it is checked without spawning anything (the four exec
// seams are not this package's to widen).
func TestFlock_LockDescriptorIsCloseOnExec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.lock")
	held, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}
	defer held.release()

	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, held.file.Fd(), uintptr(syscall.F_GETFD), 0)
	if errno != 0 {
		t.Fatalf("F_GETFD on the lock descriptor: %v", errno)
	}
	if int(flags)&syscall.FD_CLOEXEC == 0 {
		t.Fatal("the lock descriptor is not close-on-exec: a node's claude child would inherit the run's lock")
	}
}

// --- the probe's two affirmative answers -------------------------------------

// TestProbeLock_HeldWhileALegHoldsItFreeAfterward is the derivation's whole
// input: LivenessHeld while a leg holds the lock, LivenessFree once it is
// gone. Only the second, beside a leg the stream left open, may ever be read
// as abandoned.
func TestProbeLock_HeldWhileALegHoldsItFreeAfterward(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.lock")

	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if got := ProbeLock(path); got != LivenessHeld {
		t.Fatalf("ProbeLock while the lock is held = %v, want %v", got, LivenessHeld)
	}

	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := ProbeLock(path); got != LivenessFree {
		t.Fatalf("ProbeLock after release = %v, want %v", got, LivenessFree)
	}
}

// TestProbeLock_ConcurrentProbesDoNotFlickerOrBlockAcquire is why the probe is
// LOCK_SH and not LOCK_EX (ADR 0015 §1, "A shared probe, not an exclusive
// one"). Shared probes overlap freely, so two readers cannot see each other as
// a holder — under an exclusive probe, two dashboard ticks are enough to
// produce a transient false "alive" — and a probe held across a starting leg's
// acquire must not turn into a false "busy" that scales with the number of
// pollers.
func TestProbeLock_ConcurrentProbesDoNotFlickerOrBlockAcquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.lock")
	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	// One reader mid-probe: its shared lock is taken and not yet released.
	first := openForProbe(t, path)
	if err := flockSharedNB(first); err != nil {
		t.Fatalf("first shared probe: %v", err)
	}
	defer flockUnlock(first)

	if got := ProbeLock(path); got != LivenessFree {
		t.Fatalf("a second probe overlapping the first = %v, want %v (readers must not flicker)", got, LivenessFree)
	}
}

// openForProbe opens path exactly as ProbeLock does — read-only, no O_CREATE —
// and closes it when the test ends.
func openForProbe(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open %q for probing: %v", path, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// --- the filesystem gate must not fire on ordinary homes ---------------------

// TestIsLocalFilesystem_OrdinaryRunDirectoriesAreLocal guards the other
// direction of the gate. Over-strictness is safe but costly: if the allowlist
// missed the filesystem a temp dir or the repo itself sits on, every probe
// would return unknown and the whole derivation would silently never fire —
// green tests, no feature. This asserts the gate says yes where run
// directories actually live, on whatever filesystem CI and the maintainer's
// machine provide.
func TestIsLocalFilesystem_OrdinaryRunDirectoriesAreLocal(t *testing.T) {
	for _, dir := range []string{t.TempDir(), "."} {
		local, err := isLocalFilesystem(dir)
		if err != nil {
			t.Fatalf("isLocalFilesystem(%q): %v", dir, err)
		}
		if !local {
			t.Errorf("isLocalFilesystem(%q) = false — the known-local allowlist is missing this filesystem, so every probe here answers unknown", dir)
		}
	}
}

// TestIsLocalFilesystem_MissingDirectoryErrors pins that the gate reports a
// failure rather than a verdict, so ProbeLock folds it into unknown.
func TestIsLocalFilesystem_MissingDirectoryErrors(t *testing.T) {
	local, err := isLocalFilesystem(filepath.Join(t.TempDir(), "no-such-dir"))
	if err == nil {
		t.Fatal("statfs on a missing directory should fail")
	}
	if local {
		t.Fatal("a failed statfs must never report local")
	}
}
