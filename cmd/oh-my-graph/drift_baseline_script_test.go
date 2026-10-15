package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/drift-baseline.sh (#386), exercised in a scratch repository: the
// head, the lock, the requirements format and the invariant checks, with real `sh` and
// `sha256sum` only — no model CLI — so every case runs under `make test`.

const (
	driftDesign = "# Design\n\nThe tool never writes the lock.\n"
	driftReqs   = "# requirements\n\n" +
		"R1 | invariant | DESIGN.md:3 | grep -q 'never writes the lock' DESIGN.md | the lock is the owner's, not the script's\n" +
		"R2 | llm | DESIGN.md:1 | tone | the design reads plainly\n"
)

// newDriftRepo is a scratch repository holding DESIGN.md, the requirements
// file reqs and a lock over both, made with sha256sum as a person would.
func newDriftRepo(t *testing.T, reqs string) *scriptRepo {
	t.Helper()
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum not installed")
	}
	r := newScriptRepo(t)
	r.write("DESIGN.md", driftDesign)
	r.write("requirements.txt", reqs)
	r.lock()
	return r
}

// lock pins files, DESIGN.md and requirements.txt by default.
func (r *scriptRepo) lock(files ...string) {
	r.t.Helper()
	if len(files) == 0 {
		files = []string{"DESIGN.md", "requirements.txt"}
	}
	out, err := r.cmd("sha256sum", files...).Output()
	if err != nil {
		r.t.Fatalf("sha256sum: %v", err)
	}
	r.write("requirements.lock", string(out))
}

func (r *scriptRepo) remove(rel string) {
	r.t.Helper()
	if err := os.Remove(filepath.Join(r.dir, rel)); err != nil {
		r.t.Fatal(err)
	}
}

func (r *scriptRepo) wantDrift(code int, outHas ...string) string {
	r.t.Helper()
	return r.wantDriftArgs(nil, code, outHas...)
}

// wantDriftAt runs the script with head as its fourth argument.
func (r *scriptRepo) wantDriftAt(head string, code int, outHas ...string) string {
	r.t.Helper()
	return r.wantDriftArgs([]string{head}, code, outHas...)
}

func (r *scriptRepo) wantDriftArgs(extra []string, code int, outHas ...string) string {
	r.t.Helper()
	args := append([]string{"DESIGN.md", "requirements.txt", "requirements.lock"}, extra...)
	got, stdout, stderr := r.run("drift-baseline.sh", args...)
	out := stdout + stderr
	if got != code {
		r.t.Fatalf("drift-baseline exit = %d, want %d\n%s", got, code, out)
	}
	for _, s := range outHas {
		if !strings.Contains(out, s) {
			r.t.Errorf("drift-baseline output does not mention %q:\n%s", s, out)
		}
	}
	return out
}

func TestDriftBaselinePassesAGoodList(t *testing.T) {
	r := newDriftRepo(t, driftReqs)
	if out := r.wantDrift(0); strings.TrimSpace(out) != "OK 1 invariant, 1 llm" {
		t.Errorf("output = %q, want exactly OK 1 invariant, 1 llm", out)
	}
}

func TestDriftBaselineNamesAFailingInvariant(t *testing.T) {
	r := newDriftRepo(t, driftReqs+
		"R3 | invariant | DESIGN.md:9 | echo the design has no line 9; false | a line 9\n"+
		"R4 | invariant | DESIGN.md:10 | false | a silent failure\n")
	out := r.wantDrift(1, "R3 DESIGN.md:9 the design has no line 9", "R4 DESIGN.md:10 check failed")
	if strings.Contains(out, "R1") || strings.Contains(out, "OK") {
		t.Errorf("only the failing checks are named:\n%s", out)
	}
}

func TestDriftBaselineRefusesABadEntryByID(t *testing.T) {
	for name, tc := range map[string]struct{ reqs, says string }{
		"empty source":    {driftReqs + "R3 | invariant |  | true | no source\n", "R3 empty source"},
		"empty check":     {driftReqs + "R3 | llm | DESIGN.md:1 |   | no check\n", "R3 empty check"},
		"unknown measure": {driftReqs + "R3 | vibes | DESIGN.md:1 | tone | x\n", `R3 unknown measure "vibes"`},
		"duplicate ID":    {driftReqs + "R1 | llm | DESIGN.md:1 | tone | again\n", "R1 duplicate ID"},
		"too few fields":  {driftReqs + "R3 | llm | DESIGN.md:1\n", "R3 has 3 fields, want 5"},
		"| in the text":   {driftReqs + "R3 | llm | DESIGN.md:1 | tone | this | that\n", "R3 has 6 fields, want 5"},
		// Line 5: the comment, the blank line and two entries come first.
		"empty ID": {driftReqs + " | llm | DESIGN.md:1 | tone | x\n", "5 empty ID"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newDriftRepo(t, tc.reqs)
			out := r.wantDrift(1, tc.says)
			if strings.Contains(out, "OK") {
				t.Errorf("a bad list must not pass:\n%s", out)
			}
		})
	}
}

// A pipeline inside a check would be split into extra fields, so it is refused
// by ID with the fix spelled out rather than run as a truncated command.
func TestDriftBaselineRefusesAPipeInACheck(t *testing.T) {
	r := newDriftRepo(t, driftReqs+
		"R3 | invariant | DESIGN.md:3 | grep lock DESIGN.md | wc -l | counts the lock lines\n")
	out := r.wantDrift(1,
		"R3 has 6 fields, want 5 ('|' is not allowed inside a check; put a pipeline in a script file)")
	if strings.Contains(out, "OK") {
		t.Errorf("a bad list must not pass:\n%s", out)
	}
}

func TestDriftBaselineUsage(t *testing.T) {
	r := newDriftRepo(t, driftReqs)
	for _, args := range [][]string{
		{"requirements.txt", "requirements.lock"},
		{"DESIGN.md", "requirements.txt", "requirements.lock", "HEAD", "extra"},
	} {
		code, _, stderr := r.run("drift-baseline.sh", args...)
		if code != 2 || !strings.Contains(stderr, "usage:") {
			t.Errorf("args %q: exit %d, stderr %q; want 2 and a usage line", args, code, stderr)
		}
	}
}

func TestDriftBaselineRefusesAChangedDesign(t *testing.T) {
	r := newDriftRepo(t, driftReqs)
	r.write("DESIGN.md", driftDesign+"\nA line added after locking.\n")
	r.wantDrift(1, "LOCK DESIGN.md changed")
}

func TestDriftBaselineRefusesAMissingPinnedFile(t *testing.T) {
	r := newDriftRepo(t, driftReqs)
	r.remove("DESIGN.md")
	r.wantDrift(1, "LOCK DESIGN.md missing")
}

func TestDriftBaselineRefusesAnUnpinnedRequirementsFile(t *testing.T) {
	r := newDriftRepo(t, driftReqs)
	r.lock("DESIGN.md")
	out := r.wantDrift(1, "LOCK requirements.txt not pinned")
	if strings.Contains(out, "DESIGN.md") {
		t.Errorf("only the unpinned file is named:\n%s", out)
	}
}

func TestDriftBaselineRefusesAnUnpinnedDesign(t *testing.T) {
	r := newDriftRepo(t, driftReqs)
	r.lock("requirements.txt")
	r.wantDrift(1, "LOCK DESIGN.md not pinned")
}

// Both files unpinned: each is named on its own line.
func TestDriftBaselineNamesEveryUnpinnedFile(t *testing.T) {
	r := newDriftRepo(t, driftReqs)
	r.write("other.txt", "unrelated\n")
	r.lock("other.txt")
	r.wantDrift(1, "LOCK DESIGN.md not pinned\nLOCK requirements.txt not pinned\n")
}

// A lock written by `sha256sum -b` (`<hash> *<path>`) pins the same files.
func TestDriftBaselineReadsABinaryModeLock(t *testing.T) {
	r := newDriftRepo(t, driftReqs)
	r.lock("-b", "DESIGN.md", "requirements.txt")
	r.wantDrift(0, "OK 1 invariant, 1 llm")
}

// The invariants run on the working tree, so a head other than the checked-out
// commit is refused before anything runs.
func TestDriftBaselineChecksHead(t *testing.T) {
	r := newDriftRepo(t, driftReqs)
	r.commit("requirements")
	here := r.git("rev-parse", "HEAD")
	r.wantDriftAt("HEAD", 0, "OK 1 invariant, 1 llm")
	r.wantDriftAt(here, 0, "OK 1 invariant, 1 llm")

	r.commit("later")
	now := r.git("rev-parse", "HEAD")
	out := r.wantDriftAt(here, 1,
		"HEAD "+now+" is not head "+here+"; invariants run on the checked-out tree")
	if strings.Contains(out, "OK") {
		t.Errorf("a different head must not pass:\n%s", out)
	}
	r.wantDriftAt("no-such-ref", 1, "no-such-ref does not resolve")
}

func TestDriftBaselineRefusesAMissingLock(t *testing.T) {
	r := newDriftRepo(t, driftReqs)
	r.remove("requirements.lock")
	r.wantDrift(1, "LOCK requirements.lock missing")
	if r.exists("requirements.lock") {
		t.Error("the script created the lock; only a person may")
	}
}

// The lock is the owner's decision: no outcome — pass, a changed design, a
// bad entry, a failing invariant — may rewrite it.
func TestDriftBaselineNeverWritesTheLock(t *testing.T) {
	for name, tc := range map[string]struct {
		reqs, design string
		code         int
	}{
		"pass":              {driftReqs, driftDesign, 0},
		"changed design":    {driftReqs, driftDesign + "edited\n", 1},
		"bad entry":         {driftReqs + "R3 | vibes | D:1 | x | y\n", driftDesign, 1},
		"failing invariant": {driftReqs + "R3 | invariant | D:1 | false | y\n", driftDesign, 1},
	} {
		t.Run(name, func(t *testing.T) {
			r := newDriftRepo(t, tc.reqs)
			r.write("DESIGN.md", tc.design)
			before := r.read("requirements.lock")
			r.wantDrift(tc.code)
			if after := r.read("requirements.lock"); after != before {
				t.Errorf("the lock changed:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}
