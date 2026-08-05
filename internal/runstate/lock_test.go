package runstate

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// requireFlock skips a test whose subject is the flock semantics themselves.
// Off darwin and linux AcquireLock keeps the pre-ADR-0015 behaviour in full
// (O_EXCL, no marker, unlink on release), which those tests would rightly
// contradict.
func requireFlock(t *testing.T) {
	t.Helper()
	if !flockSupported {
		t.Skip("no flock(2) on this platform: the lock stays on pre-ADR-0015 semantics")
	}
}

// requireProbe skips a test that asserts an affirmative probe answer where the
// probe is designed not to give one. ADR 0015's gates — no flock(2), or a
// filesystem outside the known-local allowlist — both report unknown by
// design, and asserting past them would be asserting against the safety they
// exist to provide. It establishes the capability the same way a real caller
// would: a real lock, taken and released in the very directory under test.
func requireProbe(t *testing.T, dir string) {
	t.Helper()
	requireFlock(t)
	probe := filepath.Join(dir, "probe-capability.lock")
	release, err := AcquireLock(probe)
	if err != nil {
		t.Fatalf("acquire a capability-probe lock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release a capability-probe lock: %v", err)
	}
	if got := ProbeLock(probe); got != LivenessFree {
		t.Skipf("the lock probe cannot answer here (%v): a filesystem outside the known-local set", got)
	}
}

// --- a second acquire while the lock is held is refused -----------------------

// TestAcquireLock_SecondAcquireInSameProcessFails proves that acquiring an
// already-held lock path is refused with a *LockHeldError naming the path.
// Both acquires happen in this one test process, and that is the point rather
// than a compromise: the lock is an flock(2), which conflicts per open file
// description, so a second fd in the same process is refused exactly as
// another process would be (ADR 0015 §1 — it is why the design is flock and
// not fcntl record locks, which would grant the second acquire).
func TestAcquireLock_SecondAcquireInSameProcessFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.lock")

	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	defer release()

	_, err = AcquireLock(path)
	var held *LockHeldError
	if !errors.As(err, &held) {
		t.Fatalf("second AcquireLock = %T: %v, want *LockHeldError", err, err)
	}
	if held.Path != path {
		t.Fatalf("LockHeldError.Path = %q, want %q", held.Path, path)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error should name the lock path: %v", err)
	}
}

// --- release frees the path for a subsequent acquire -------------------------

func TestAcquireLock_ReleaseAllowsReacquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.lock")

	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	release2, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	defer release2()
}

// --- release leaves the file behind, on purpose ------------------------------

// TestAcquireLock_ReleaseKeepsTheLockFile pins the sharpest edge of ADR 0015
// §1: the release path unlocks and closes but must NOT unlink. Acquiring is
// open-then-flock, so an unlink between those two steps lets one caller hold
// the lock on an orphaned inode while another takes it, uncontended, on a
// freshly created one — two schedulers, one run, both spending. The file
// therefore stays as a permanent, inert resident of the run directory, and its
// existence carries no state.
func TestAcquireLock_ReleaseKeepsTheLockFile(t *testing.T) {
	requireFlock(t)
	path := filepath.Join(t.TempDir(), "resume.lock")

	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("a released lock file must remain on disk: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("the released lock file is empty; it must keep the marker a reader identifies the format by")
	}
}

// --- the lock file's format: marker line, then the informational pid ---------

func TestAcquireLock_FileCarriesTheMarkerThenThePID(t *testing.T) {
	requireFlock(t)
	path := filepath.Join(t.TempDir(), "resume.lock")
	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer release()

	lines := lockFileLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("lock file = %q, want exactly a marker line and a pid line", lines)
	}
	if lines[0] != LockFormatMarker {
		t.Fatalf("first line = %q, want the format marker %q", lines[0], LockFormatMarker)
	}
	got, err := strconv.Atoi(lines[1])
	if err != nil {
		t.Fatalf("second line %q is not a pid: %v", lines[1], err)
	}
	if got != os.Getpid() {
		t.Fatalf("lock file pid = %d, want %d", got, os.Getpid())
	}
}

// TestAcquireLock_RewriteTruncatesThePreviousHolder proves the rewrite
// truncates. With O_EXCL gone a lock file is reopened and reused, so a longer
// previous body would otherwise leave its tail behind and LockHeldError would
// quote a stale pid at the next contended acquire.
func TestAcquireLock_RewriteTruncatesThePreviousHolder(t *testing.T) {
	requireFlock(t)
	path := filepath.Join(t.TempDir(), "resume.lock")

	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// A previous holder with a much longer body than this process's.
	if err := os.WriteFile(path, []byte(LockFormatMarker+"\n999999999\ntrailing-garbage\n"), 0o644); err != nil {
		t.Fatalf("write a long previous body: %v", err)
	}

	release2, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("re-acquire over a long previous body: %v", err)
	}
	defer release2()

	lines := lockFileLines(t, path)
	want := []string{LockFormatMarker, strconv.Itoa(os.Getpid())}
	if len(lines) != len(want) || lines[0] != want[0] || lines[1] != want[1] {
		t.Fatalf("lock file after re-acquire = %q, want %q — the rewrite must truncate", lines, want)
	}
}

// --- an unmarked lock file gets the legacy semantics its writer knew ---------

// TestAcquireLock_UnmarkedLockFileIsRefusedUnderLegacySemantics is the half of
// ADR 0015 §4 that makes the upgrade safe. A lock file without the marker was
// written by a pre-flock binary, whose live leg holds NO flock: taking an
// uncontended LOCK_EX against it would double-run the run. So it is refused
// under the only semantics its writer knew — existence is the lock, a human
// decides — and the message keeps the exact path to delete.
func TestAcquireLock_UnmarkedLockFileIsRefusedUnderLegacySemantics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.lock")
	// Exactly what a pre-ADR-0015 binary wrote: the pid, nothing else.
	if err := os.WriteFile(path, []byte("4321\n"), 0o644); err != nil {
		t.Fatalf("write a pre-ADR lock file: %v", err)
	}

	_, err := AcquireLock(path)
	var held *LockHeldError
	if !errors.As(err, &held) {
		t.Fatalf("AcquireLock over a pre-ADR lock file = %T: %v, want *LockHeldError", err, err)
	}
	if !held.Legacy {
		t.Fatal("an unmarked lock file must be refused under legacy semantics, not flock semantics")
	}
	if !strings.Contains(err.Error(), "rm "+strconv.Quote(path)) {
		t.Fatalf("the legacy refusal must keep the exact path to delete: %v", err)
	}

	// Self-expiring: once the legacy lock is cleared, the next acquire writes a
	// marked one and the run is on flock semantics forever.
	requireFlock(t)
	if err := os.Remove(path); err != nil {
		t.Fatalf("clear the legacy lock: %v", err)
	}
	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("acquire after the legacy lock was cleared: %v", err)
	}
	defer release()
	if lines := lockFileLines(t, path); lines[0] != LockFormatMarker {
		t.Fatalf("first line = %q, want the marker — the legacy arm must expire", lines[0])
	}
}

// --- what a contended refusal says -------------------------------------------

// TestLockHeldError_FlockArmNamesThePIDAndRefusesToAdviseRM pins the wording
// ADR 0015 §4 requires. Under flock semantics "delete it and retry" is an
// active double-spend footgun — unlinking does not release the live holder's
// lock, while the next leg takes an uncontended one on a fresh inode — so the
// advice is gone, and the pid appears as a label for a process the flock has
// already proved alive, never as a target.
func TestLockHeldError_FlockArmNamesThePIDAndRefusesToAdviseRM(t *testing.T) {
	requireFlock(t)
	path := filepath.Join(t.TempDir(), "resume.lock")
	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	defer release()

	_, err = AcquireLock(path)
	var held *LockHeldError
	if !errors.As(err, &held) {
		t.Fatalf("second AcquireLock = %T: %v, want *LockHeldError", err, err)
	}
	if held.Legacy {
		t.Fatal("a marked, flocked lock must be refused under flock semantics")
	}
	if held.PID != os.Getpid() {
		t.Fatalf("LockHeldError.PID = %d, want the holder's pid %d", held.PID, os.Getpid())
	}
	msg := err.Error()
	if !strings.Contains(msg, "in flight") || !strings.Contains(msg, strconv.Itoa(os.Getpid())) {
		t.Fatalf("the flock refusal should name the in-flight holder: %v", msg)
	}
	if strings.Contains(msg, "rm ") || strings.Contains(msg, "delete it") {
		t.Fatalf("the flock refusal must not advise deleting the lock: %v", msg)
	}
	if strings.Contains(msg, "kill") {
		t.Fatalf("the pid is a label, not a target: %v", msg)
	}
}

// --- AcquireLock creates its parent directory --------------------------------

func TestAcquireLock_CreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".oh-my-graph", "runs", "run-1", "resume.lock")
	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock into a missing directory tree: %v", err)
	}
	defer release()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}
}

// --- every ambiguity is unknown ----------------------------------------------

// TestProbeLock_AmbiguityIsUnknown walks the cases ADR 0015 §1 folds into
// unknown. Each of them is a possible LIVE run, and unknown is what makes a
// reader answer exactly as it did before ADR 0015 (an open leg reads as in
// flight) instead of authorising a second scheduler over it.
func TestProbeLock_AmbiguityIsUnknown(t *testing.T) {
	dir := t.TempDir()

	// A pre-ADR lock file naming a pid that DOES exist. This process's own pid
	// is the only one a test can be sure about, and it is the shape that
	// matters: alive is inconclusive (holder, or a stranger that recycled the
	// number — ADR 0015, Context), and inconclusive is unknown.
	unmarkedAlive := filepath.Join(dir, "unmarked-alive.lock")
	if err := os.WriteFile(unmarkedAlive, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatalf("write a pre-ADR lock file: %v", err)
	}
	unmarkedGarbage := filepath.Join(dir, "unmarked-garbage.lock")
	if err := os.WriteFile(unmarkedGarbage, []byte("not-a-pid\n"), 0o644); err != nil {
		t.Fatalf("write a malformed pre-ADR lock file: %v", err)
	}
	empty := filepath.Join(dir, "empty.lock")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatalf("write an empty lock file: %v", err)
	}
	wrongMarker := filepath.Join(dir, "wrong-marker.lock")
	if err := os.WriteFile(wrongMarker, []byte("oh-my-graph-lock 2\n4321\n"), 0o644); err != nil {
		t.Fatalf("write a future-format lock file: %v", err)
	}

	cases := []struct {
		name string
		path string
	}{
		{"a missing lock file — a pre-ADR run directory, or one a human cleaned", filepath.Join(dir, "absent.lock")},
		{"a missing run directory entirely", filepath.Join(dir, "no-such-run", "resume.lock")},
		{"a pre-ADR lock file whose pid still names a process", unmarkedAlive},
		{"a pre-ADR lock file this package cannot read a pid out of", unmarkedGarbage},
		{"an empty lock file", empty},
		{"a lock file written in a format this binary does not know", wrongMarker},
	}
	for _, tc := range cases {
		if got := ProbeLock(tc.path); got != LivenessUnknown {
			t.Errorf("ProbeLock(%s) = %v, want %v", tc.name, got, LivenessUnknown)
		}
	}
}

// --- the legacy path: an unmarked lock, decided in one direction only --------

// deadPID is a pid no process can bear. linux caps pid_max at 2^22 and darwin
// at 99999, so kill(2) answers ESRCH for this on both — which is what lets a
// test assert the "gone" branch without spawning anything (a spawn here would
// be a fifth exec seam in test clothing, and the design forbids one for exactly
// this question).
const deadPID = 2147483647

// TestProbeLock_LegacyLockWithAGonePIDIsFree is the case both preserved
// specimens of ADR 0015's Context are: `<pid>\n`, no marker, and the pid long
// since gone. Folding it into unknown made those runs read RUNNING forever,
// with no later moment at which they could become diagnosable, because the
// marker will never appear in a file no live binary writes.
func TestProbeLock_LegacyLockWithAGonePIDIsFree(t *testing.T) {
	dir := t.TempDir()
	requireProbe(t, dir)
	path := filepath.Join(dir, "resume.lock")
	if err := os.WriteFile(path, []byte(strconv.Itoa(deadPID)+"\n"), 0o644); err != nil {
		t.Fatalf("write a pre-ADR lock file: %v", err)
	}
	if got := ProbeLock(path); got != LivenessFree {
		t.Fatalf("ProbeLock over a pre-ADR lock naming a gone pid = %v, want %v", got, LivenessFree)
	}
}

// TestPIDGone_OnlyESRCHIsEvidence pins the asymmetry the legacy path rests on.
// Everything except "no process bears this pid" is inconclusive, and pid <= 0
// must never reach kill(2), where 0 addresses the caller's own process group
// and a negative value addresses group n — either would answer a question
// nobody asked, and the first would answer it by finding this test process.
func TestPIDGone_OnlyESRCHIsEvidence(t *testing.T) {
	cases := []struct {
		name string
		pid  int
		want bool
	}{
		{"a pid no process bears", deadPID, true},
		{"this very process", os.Getpid(), false},
		{"pid 0 — the caller's own process group, never asked", 0, false},
		{"a negative pid — a process group, never asked", -os.Getpid(), false},
	}
	for _, tc := range cases {
		if got := pidGone(tc.pid); got != tc.want {
			t.Errorf("pidGone(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestLegacyLockPID_AcceptsOnlyAPreFlockBody guards the one place in this
// design where a file's CONTENTS decide that a run is gone. Anything that is
// not exactly what acquireLegacyLock writes must be refused, so that a file
// some other writer left behind — or a marked lock caught mid-rewrite, which is
// momentarily empty — can never be read as a dead leg.
func TestLegacyLockPID_AcceptsOnlyAPreFlockBody(t *testing.T) {
	cases := []struct {
		head    string
		wantPID int
		wantOK  bool
	}{
		{"99429\n", 99429, true},
		{"99429", 99429, true},     // no trailing newline
		{"99429\r\n", 99429, true}, // a CRLF-ish tail
		{"", 0, false},             // a marked lock caught between Truncate and Write
		{"not-a-pid\n", 0, false},
		{"0\n", 0, false},
		{"-1\n", 0, false},
		{"99429\n80834\n", 0, false},         // a second line: not this format
		{"99429 extra\n", 0, false},          // junk on the pid line
		{strings.Repeat("9", 200), 0, false}, // longer than the bounded head
	}
	for _, tc := range cases {
		pid, ok := legacyLockPID([]byte(tc.head))
		if pid != tc.wantPID || ok != tc.wantOK {
			t.Errorf("legacyLockPID(%q) = (%d, %v), want (%d, %v)", tc.head, pid, ok, tc.wantPID, tc.wantOK)
		}
	}
}

// TestAcquireLock_LegacyRefusalShowsAGonePIDButStillRefuses is the half of this
// that deliberately did NOT change. A reader may call such a run abandoned; the
// acquire path may not act on that, because a false "gone" here would start a
// second leg over a live one and spend money. So the refusal stands, and the
// pid only improves the evidence the human deciding about `rm` is given.
func TestAcquireLock_LegacyRefusalShowsAGonePIDButStillRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.lock")
	if err := os.WriteFile(path, []byte(strconv.Itoa(deadPID)+"\n"), 0o644); err != nil {
		t.Fatalf("write a pre-ADR lock file: %v", err)
	}

	_, err := AcquireLock(path)
	var held *LockHeldError
	if !errors.As(err, &held) {
		t.Fatalf("AcquireLock over a pre-ADR lock file = %T: %v, want *LockHeldError", err, err)
	}
	if !held.Legacy {
		t.Fatal("an unmarked lock file must still be refused under legacy semantics")
	}
	if !strings.Contains(err.Error(), "rm "+strconv.Quote(path)) {
		t.Fatalf("the legacy refusal must keep the exact path to delete: %v", err)
	}
	if !pidGone(deadPID) {
		return // no kill(2) here; the evidence half of this test does not apply
	}
	if !held.PIDGone || held.PID != deadPID {
		t.Fatalf("LockHeldError = %+v, want PID %d reported gone", held, deadPID)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(deadPID)) {
		t.Fatalf("the refusal must show the gone pid as evidence: %v", err)
	}
}

// TestAcquireLock_LegacyRefusalHidesALivePID is the other direction. A pid that
// still names a process proves nothing about a pre-flock lock — it may be a
// stranger that recycled the number — so it must not be printed as though it
// did, and the message stays the one this tool has always given.
func TestAcquireLock_LegacyRefusalHidesALivePID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.lock")
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatalf("write a pre-ADR lock file: %v", err)
	}

	_, err := AcquireLock(path)
	var held *LockHeldError
	if !errors.As(err, &held) {
		t.Fatalf("AcquireLock over a pre-ADR lock file = %T: %v, want *LockHeldError", err, err)
	}
	if held.PIDGone || held.PID != 0 {
		t.Fatalf("LockHeldError = %+v, want a live pid left out entirely", held)
	}
	if strings.Contains(err.Error(), strconv.Itoa(os.Getpid())) {
		t.Fatalf("a live pid is not evidence on the legacy arm and must not be shown: %v", err)
	}
}

// TestLiveness_ZeroValueIsUnknown pins the direction of the whole design: a
// Liveness nobody set must never claim a run is dead.
func TestLiveness_ZeroValueIsUnknown(t *testing.T) {
	var zero Liveness
	if zero != LivenessUnknown {
		t.Fatalf("the zero Liveness = %v, want %v", zero, LivenessUnknown)
	}
	if zero.String() != "unknown" {
		t.Fatalf("the zero Liveness renders %q, want %q", zero.String(), "unknown")
	}
}

// lockFileLines reads the lock file at path as its non-empty lines.
func lockFileLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
