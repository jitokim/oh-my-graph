package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/drift-baseline.sh (#386), exercised in a scratch repository: the
// lock, the requirements format and the invariant checks, with real `sh` and
// `sha256sum` only — no model CLI — so every case runs under `make test`.

const (
	driftDesign = "# Design\n\nThe tool never writes the lock.\n"
	driftReqs   = "# requirements\n\n" +
		"R1 | invariant | DESIGN.md:3 | grep -q 'never writes the lock' DESIGN.md | the lock is the owner's | not the script's\n" +
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

func (r *scriptRepo) lock() {
	r.t.Helper()
	out, err := r.cmd("sha256sum", "DESIGN.md", "requirements.txt").Output()
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
	got, stdout, stderr := r.run("drift-baseline.sh", "requirements.txt", "requirements.lock")
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
		"too few fields":  {driftReqs + "R3 | llm | DESIGN.md:1\n", "R3 fewer than five"},
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
