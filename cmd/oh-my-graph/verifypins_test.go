package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// #363: `auto --verify-cmd` pins the scripts its command line executes once per
// invocation, and a verification whose pinned file changed is a fault naming
// the file. The sinks here run the real ShellVerifier on a shebang script in a
// temp dir (as buildevidence_test.go's do); the model is always a FakeRunner,
// and the baseline a FakeVerifier, so no claude is ever spawned.

// editedScript is a verify script that still exits 0: an edit that would pass
// if the run took it, which is exactly the edit pinning has to refuse.
const editedScript = "#!/bin/sh\n# a planned node was here\nexit 0\n"

// onNodeCall runs fn when the FakeRunner launches the call keyed key (by
// newCycleFake's KeyFn, or the prompt when there is none) — the moment a planned node (or the assessor) holding the stock Edit
// grant could rewrite a file.
func onNodeCall(fake *runner.FakeRunner, key string, fn func()) {
	keyFn := fake.KeyFn
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		k := spec.Prompt
		if keyFn != nil {
			k = keyFn(spec)
		}
		if k == key {
			fn()
		}
		return k
	}
}

// writeFile overwrites path, keeping an executable bit for a script.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// resolved is a pin's Path for file: on macOS t.TempDir sits behind the
// /var -> /private/var symlink, and a pin follows every symlink.
func resolved(t *testing.T, file string) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatalf("resolve %s: %v", file, err)
	}
	return path
}

func sha256Of(t *testing.T, file string) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// greenBaselineFor is a FakeVerifier answering exit 0 for command.
func greenBaselineFor(command string) *verify.FakeVerifier {
	return verify.NewFakeVerifier(map[string]verify.Result{command: {ExitCode: 0}})
}

// oneCycle scripts a single-cycle auto run whose work node passes.
func oneCycle() *runner.FakeRunner {
	return newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})
}

// selfWriteHint is what a pin fault found by the post-check of one
// verification adds before the file (#367).
const selfWriteHint = "is this a file the command itself writes?"

// wantSinkPinFault asserts the sink's record is a FAIL whose detail ENDS by
// naming file as the pinned file that changed since launch — no self-write
// hint, the change came between verifications — and whose verification is
// not a pass.
func wantSinkPinFault(t *testing.T, rec runstate.NodeRecord, file string) {
	t.Helper()
	wantSinkFaultEnding(t, rec, "changed since launch: pinned file "+file)
	if strings.Contains(rec.Detail, selfWriteHint) {
		t.Errorf("a change made between verifications carries the self-write hint: %q", rec.Detail)
	}
}

// wantSinkFaultEnding asserts the sink's record is a FAIL whose detail ends
// with tail and whose verification is not a pass.
func wantSinkFaultEnding(t *testing.T, rec runstate.NodeRecord, tail string) {
	t.Helper()
	if rec.Verdict != runstate.VerdictFail {
		t.Errorf("sink verdict = %q, want FAIL — a changed pinned file is never a PASS (detail %q)", rec.Verdict, rec.Detail)
	}
	if !strings.HasSuffix(rec.Detail, tail) {
		t.Errorf("sink detail %q does not end %q", rec.Detail, tail)
	}
	if rec.Verification == nil || rec.Verification.Status == runstate.VerificationPassed {
		t.Errorf("sink verification = %+v, want a record that did not pass", rec.Verification)
	}
}

func wantSinkPass(t *testing.T, rec runstate.NodeRecord) {
	t.Helper()
	if rec.Verdict != runstate.VerdictPass {
		t.Errorf("sink verdict = %q, want PASS (detail %q)", rec.Verdict, rec.Detail)
	}
	if rec.Verification == nil || rec.Verification.Status != runstate.VerificationPassed {
		t.Errorf("sink verification = %+v, want passed", rec.Verification)
	}
}

// (a) An edit planted between launch and the sink — by the planned node
// itself — faults the sink, naming the file, even though the edited script
// still exits 0.
func TestRunAuto_EditBetweenLaunchAndSinkFaults_363(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	fake := oneCycle()
	onNodeCall(fake, "work-1", func() { writeFile(t, script, editedScript) })

	out, err := runBaselineAuto(t, fake, greenBaselineFor(script), osStdin(), "add a README section", "--verify-cmd", script)

	if err == nil {
		t.Fatalf("a run whose verify script was edited mid-run succeeded:\n%s", out)
	}
	wantSinkPinFault(t, loadSnapshot(t, soleRunID(t)).Nodes["work"], resolved(t, script))
}

// (b) An unchanged file passes exactly as before, and the pin is recorded
// in state.json as word, resolved path and digest.
func TestRunAuto_UnchangedPinnedFilePasses_363(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	digest := sha256Of(t, script)

	out, err := runBaselineAuto(t, oneCycle(), greenBaselineFor(script), osStdin(), "add a README section", "--verify-cmd", script)

	if err != nil {
		t.Fatalf("an untouched verify script must pass: %v\n%s", err, out)
	}
	snap := loadSnapshot(t, soleRunID(t))
	wantSinkPass(t, snap.Nodes["work"])
	want := []runstate.VerifyPin{{Word: script, Path: resolved(t, script), SHA256: digest}}
	if !reflect.DeepEqual(snap.VerifyPins, want) {
		t.Errorf("verify_pins = %+v, want %+v", snap.VerifyPins, want)
	}
}

// (c) A word naming a missing or unreadable file is not pinned, so creating
// the missing one mid-run raises no false fault.
func TestRunAuto_MissingOrUnreadableArgumentIsNotPinned_363(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	dir := t.TempDir()
	missing := filepath.Join(dir, "later.txt")
	command := script + " " + missing
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		unreadable := filepath.Join(dir, "secret.txt")
		if err := os.WriteFile(unreadable, []byte("x"), 0o000); err != nil {
			t.Fatal(err)
		}
		command += " " + unreadable
	}
	fake := oneCycle()
	onNodeCall(fake, "work-1", func() { writeFile(t, missing, "now it exists\n") })

	out, err := runBaselineAuto(t, fake, greenBaselineFor(command), osStdin(), "add a README section", "--verify-cmd", command)

	if err != nil {
		t.Fatalf("an unpinnable argument must not fault the run: %v\n%s", err, out)
	}
	snap := loadSnapshot(t, soleRunID(t))
	wantSinkPass(t, snap.Nodes["work"])
	if len(snap.VerifyPins) != 1 || snap.VerifyPins[0].Word != script {
		t.Errorf("verify_pins = %+v, want only the script", snap.VerifyPins)
	}
}

// (d) A symlink re-pointed after launch faults, even at a file whose content
// is byte-identical: the pin is the resolved path as well as the digest.
func TestRunAuto_RepointedSymlinkFaults_363(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	isolateRunHome(t)
	dir := t.TempDir()
	original, twin := filepath.Join(dir, "a.sh"), filepath.Join(dir, "b.sh")
	writeFile(t, original, "#!/bin/sh\nexit 0\n")
	writeFile(t, twin, "#!/bin/sh\nexit 0\n")
	link := filepath.Join(dir, "check.sh")
	if err := os.Symlink(original, link); err != nil {
		t.Fatal(err)
	}
	fake := oneCycle()
	onNodeCall(fake, "work-1", func() {
		if err := os.Remove(link); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(twin, link); err != nil {
			t.Error(err)
		}
	})

	_, err := runBaselineAuto(t, fake, greenBaselineFor(link), osStdin(), "add a README section", "--verify-cmd", link)

	if err == nil {
		t.Fatal("a re-pointed verify symlink must fault the run")
	}
	rec := loadSnapshot(t, soleRunID(t)).Nodes["work"]
	wantSinkPinFault(t, rec, resolved(t, original))
	// The detail keeps the fault's tail, which ends with the new target.
	if !strings.Contains(rec.Detail, string(filepath.Separator)+"b.sh; changed since launch") {
		t.Errorf("detail %q does not say where the path now points", rec.Detail)
	}
}

// (e) A file edited DURING the verification — here the verify script, by
// itself, which exits 0 — faults from the post-check. (Its arguments are
// not pinned since #367, so the script must rewrite itself.) The fault asks
// whether the command writes the file, and still ends with the file (#367).
func TestRunAuto_EditDuringVerificationFaults_363(t *testing.T) {
	isolateRunHome(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "check.sh")
	writeFile(t, script, "#!/bin/sh\necho '# tampered' >> \"$0\"\nexit 0\n")

	_, err := runBaselineAuto(t, oneCycle(), greenBaselineFor(script), osStdin(), "add a README section", "--verify-cmd", script)

	if err == nil {
		t.Fatal("a verification that edited its own pinned file must fault")
	}
	wantSinkFaultEnding(t, loadSnapshot(t, soleRunID(t)).Nodes["work"], selfWriteHint+" pinned file "+resolved(t, script))
}

// (f) An edit between cycle 1 and cycle 2 of a --max-cycles run faults cycle
// 2's verification: nothing re-pins per cycle, so cycle 1's edit can never
// become cycle 2's pin, and both cycles record the launch's one pin.
func TestRunAuto_EditBetweenCyclesFaultsTheLaterCycle_363(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	digest := sha256Of(t, script)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1":   {SessionID: "s-work-1", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-1": {Result: cycleAssessNotMet, TotalCostUSD: 0.02},
		"plan-2":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-2":   {SessionID: "s-work-2", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-2": {Result: cycleAssessMet, TotalCostUSD: 0.02},
	})
	onNodeCall(fake, "assess-1", func() { writeFile(t, script, editedScript) })

	out, err := runBaselineAuto(t, fake, greenBaselineFor(script), osStdin(), "add a README section", "--verify-cmd", script, "--max-cycles", "2")

	if err == nil {
		t.Fatalf("a goal loop whose verify script changed between cycles succeeded:\n%s", out)
	}
	snaps := goalSnapshots(t)
	if len(snaps) != 2 {
		t.Fatalf("%d cycle snapshots, want 2", len(snaps))
	}
	wantSinkPass(t, snaps[0].Nodes["work"])
	wantSinkPinFault(t, snaps[1].Nodes["work"], resolved(t, script))
	want := []runstate.VerifyPin{{Word: script, Path: resolved(t, script), SHA256: digest}}
	for i, snap := range snaps {
		if !reflect.DeepEqual(snap.VerifyPins, want) {
			t.Errorf("cycle %d verify_pins = %+v, want the launch's %+v", i+1, snap.VerifyPins, want)
		}
	}
}

// (g) The starting-tree baseline is guarded by the same pins: a script
// edited while the baseline runs it is refused, naming the file, before
// anything is planned or billed.
func TestRunAuto_BaselineIsGuardedByThePins_363(t *testing.T) {
	home := isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	fake := oneCycle()
	baseline := greenBaselineFor(script)
	baseline.OnVerify(script, func(context.Context) { writeFile(t, script, editedScript) })

	out, err := runBaselineAuto(t, fake, baseline, osStdin(), "add a README section", "--verify-cmd", script)

	var red *BaselineRedError
	if !errors.As(err, &red) {
		t.Fatalf("want a baseline refusal, got %T: %v\n%s", err, err, out)
	}
	var pinErr *verify.PinChangedError
	if !errors.As(err, &pinErr) || pinErr.Path != resolved(t, script) {
		t.Errorf("baseline refusal %v does not carry the pin fault for %s", err, resolved(t, script))
	}
	assertNothingSpent(t, home, fake)
}

// (g) --no-baseline skips the baseline, not the pinning: the sinks are
// guarded and the pins recorded all the same.
func TestRunAuto_NoBaselineStillPins_363(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	fake := oneCycle()
	onNodeCall(fake, "work-1", func() { writeFile(t, script, editedScript) })

	_, err := runBaselineAuto(t, fake, verify.NewFakeVerifier(nil), osStdin(), "add a README section", "--no-baseline", "--verify-cmd", script)

	if err == nil {
		t.Fatal("--no-baseline must not switch the pins off")
	}
	wantSinkPinFault(t, loadSnapshot(t, soleRunID(t)).Nodes["work"], resolved(t, script))
}

// (h) The pins recorded in state.json survive a resume — a resumed leg's own
// writes keep them, so a second resume still has them — and a file changed
// while the run was stopped faults on the resumed leg.
func TestResume_RecordedPinsSurviveAndFaultAChangeWhileStopped_363(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1": {ExitCode: 1, FailureCause: limitCauseMsg, SessionLimited: true},
		"work-2": {ExitCode: 1, FailureCause: limitCauseMsg, SessionLimited: true},
		"work-3": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})

	_, err := runBaselineAuto(t, fake, greenBaselineFor(script), osStdin(), "add a README section", "--verify-cmd", script)
	var limited *schedule.LimitPausedError
	if !errors.As(err, &limited) {
		t.Fatalf("leg 1 should pause on the session limit, got %T: %v", err, err)
	}
	runID := soleRunID(t)
	pins := loadSnapshot(t, runID).VerifyPins
	if len(pins) != 1 || pins[0].Path != resolved(t, script) {
		t.Fatalf("leg 1 recorded verify_pins %+v, want the script", pins)
	}

	resume := func() error {
		var resumeErr error
		captureStdout(t, func() {
			resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed", "--verify-cmd", script}), fake, browser.NewFakeOpener())
		})
		return resumeErr
	}
	if err := resume(); !errors.As(err, &limited) {
		t.Fatalf("leg 2 should pause on the session limit again, got %T: %v", err, err)
	}
	if got := loadSnapshot(t, runID).VerifyPins; !reflect.DeepEqual(got, pins) {
		t.Fatalf("leg 2's writes changed the recorded pins: %+v, want %+v", got, pins)
	}

	writeFile(t, script, editedScript)
	if err := resume(); err == nil {
		t.Fatal("leg 3 passed a verify script edited while the run was stopped")
	}
	snap := loadSnapshot(t, runID)
	wantSinkPinFault(t, snap.Nodes["work"], resolved(t, script))
	if !reflect.DeepEqual(snap.VerifyPins, pins) {
		t.Errorf("leg 3 re-pinned: %+v, want the launch's %+v", snap.VerifyPins, pins)
	}
}

// (i) A state.json written before #363, with no verify_pins key, resumes
// exactly as today: nothing to check, so an edited (still green) script
// passes, and the resumed leg writes no key either.
func TestResume_SnapshotWithoutPinsResumesUnchanged_363(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1": {ExitCode: 1, FailureCause: limitCauseMsg, SessionLimited: true},
		"work-2": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})
	_, err := runBaselineAuto(t, fake, greenBaselineFor(script), osStdin(), "add a README section", "--verify-cmd", script)
	var limited *schedule.LimitPausedError
	if !errors.As(err, &limited) {
		t.Fatalf("leg 1 should pause on the session limit, got %T: %v", err, err)
	}
	runID := soleRunID(t)
	old := loadSnapshot(t, runID)
	old.VerifyPins = nil
	if err := runstate.Write(statePath(runID), old); err != nil {
		t.Fatal(err)
	}
	writeFile(t, script, editedScript)

	var resumeErr error
	captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed", "--verify-cmd", script}), fake, browser.NewFakeOpener())
	})
	if resumeErr != nil {
		t.Fatalf("a pre-#363 snapshot must resume as before: %v", resumeErr)
	}
	wantSinkPass(t, loadSnapshot(t, runID).Nodes["work"])
	raw, err := os.ReadFile(statePath(runID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"verify_pins"`) {
		t.Errorf("the resumed leg invented a verify_pins key:\n%s", raw)
	}
}

// (j, cmd side) An auto run without --verify-cmd writes no verify_pins key.
func TestRunAuto_NoVerifyCmdWritesNoPins_363(t *testing.T) {
	isolateRunHome(t)
	t.Chdir(t.TempDir())

	out, err := runBaselineAuto(t, oneCycle(), verify.NewFakeVerifier(nil), osStdin(), "add a README section")

	if err != nil {
		t.Fatalf("auto without --verify-cmd: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(statePath(soleRunID(t)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"verify_pins"`) {
		t.Errorf("a run without --verify-cmd recorded verify_pins:\n%s", raw)
	}
}

// A hand-written `run` graph is untouched: its own verify is the user's
// reviewed artifact, nothing is pinned, and an edit to the file it names
// changes nothing about the verdict.
func TestRunGraph_HandWrittenVerifyIsNotPinned_363(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	g := mustParse(t, `{"name":"hand","nodes":[{"id":"work","prompt":"work","success_check":{"verify":{"command":"`+script+`"}}}]}`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"work": passedOutcome("work")})
	onNodeCall(fake, "work", func() { writeFile(t, script, editedScript) })

	var err error
	captureStdout(t, func() {
		err = executeGraph(context.Background(), "run-hand-363", g, fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "hand.yaml", []byte("name: hand\n"), false, nil, nil, nil)
	})
	if err != nil {
		t.Fatalf("a hand-written run must be unaffected by #363: %v", err)
	}
	snap := loadSnapshot(t, "run-hand-363")
	wantSinkPass(t, snap.Nodes["work"])
	if snap.VerifyPins != nil {
		t.Errorf("a hand-written run recorded verify_pins %+v", snap.VerifyPins)
	}
}

// The wrapper is absent, not merely inert, wherever there is nothing pinned.
func TestVerifyPinSet_NilGuardsNothing_363(t *testing.T) {
	inner := verify.NewFakeVerifier(nil)
	var none *verifyPinSet
	if got := none.guard(inner); got != verify.Verifier(inner) {
		t.Errorf("a nil set wrapped the verifier: %T", got)
	}
	if none.record() != nil {
		t.Error("a nil set recorded pins")
	}
	if pinVerifyCommand(coordinator.VerifyCommand{}) != nil {
		t.Error("no --verify-cmd still pinned")
	}
	if resumedVerifyPins("./check.sh", nil) != nil {
		t.Error("a snapshot with no pins still built a set")
	}
	if got := (&verifyPinSet{command: "make"}).guard(inner); got != verify.Verifier(inner) {
		t.Errorf("a command naming no file wrapped the verifier: %T", got)
	}
}

// (a') A --verify-cmd carrying a {{ }} token is interpolated at the sink, so
// the Request the verifier sees is not the string that was pinned. It is
// still the user's command, and an edit to the file it names still faults;
// left alone, it still passes.
func TestRunAuto_InterpolatedCommandIsStillGuarded_363(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "edited"}[edit], func(t *testing.T) {
			isolateRunHome(t)
			script := writeVerifyScript(t, 0)
			command := script + " --within {{ self.timeout }}"
			fake := oneCycle()
			if edit {
				onNodeCall(fake, "work-1", func() { writeFile(t, script, editedScript) })
			}

			out, err := runBaselineAuto(t, fake, greenBaselineFor(command), osStdin(), "add a README section", "--verify-cmd", command)

			rec := loadSnapshot(t, soleRunID(t)).Nodes["work"]
			if !edit {
				if err != nil {
					t.Fatalf("an untouched verify script must pass: %v\n%s", err, out)
				}
				wantSinkPass(t, rec)
				return
			}
			if err == nil {
				t.Fatalf("an interpolated command whose script was edited succeeded:\n%s", out)
			}
			wantSinkPinFault(t, rec, resolved(t, script))
		})
	}
}

// (k) The plan screen lists, under the build-evidence block, the absolute
// path of every file the launch pinned (#363) — for a script handed to an
// interpreter and for one run by path alike (#367).
func TestRunAuto_PlanScreenListsPinnedPaths_363(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the evidence command here is a shell script")
	}
	for _, command := range []string{"sh v.sh", "./v.sh", "sh v.sh && ./w.sh"} {
		t.Run(command, func(t *testing.T) {
			isolateRunHome(t)
			dir := t.TempDir()
			t.Chdir(dir)
			writeFile(t, filepath.Join(dir, "v.sh"), "#!/bin/sh\nexit 0\n")
			writeFile(t, filepath.Join(dir, "w.sh"), "#!/bin/sh\nexit 0\n")

			out, err := runBaselineAuto(t, oneCycle(), greenBaselineFor(command), osStdin(), "add a README section", "--verify-cmd", command)

			if err != nil {
				t.Fatalf("an untouched verify script must pass: %v\n%s", err, out)
			}
			pins := loadSnapshot(t, soleRunID(t)).VerifyPins
			if len(pins) == 0 {
				t.Fatalf("%q pinned nothing", command)
			}
			block := out[strings.Index(out, "build evidence (--verify-cmd)"):]
			for _, pin := range pins {
				if !filepath.IsAbs(pin.Path) {
					t.Errorf("pin path %q is not absolute", pin.Path)
				}
				if want := "\n    pinned: " + pin.Path + "\n"; !strings.Contains(block, want) {
					t.Errorf("plan screen does not list %q under the build-evidence block:\n%s", want, out)
				}
			}
			if strings.Contains(out, "verify-cmd has no pinned script") {
				t.Errorf("a command that pinned %d file(s) printed the no-pin warning:\n%s", len(pins), out)
			}
		})
	}
}

// (l) `cd sub && sh v.sh` pins nothing — paths resolve from the launch
// directory only, and v.sh is only in sub/ (#367) — so the plan screen warns
// that nothing is guarded instead of staying silent, and the run's
// verification still passes.
func TestRunAuto_UnpinnableCommandWarnsAndPasses_363(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the evidence command here is a shell script")
	}
	isolateRunHome(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "sub", "v.sh"), "#!/bin/sh\nexit 0\n")
	command := "cd sub && sh v.sh"

	out, err := runBaselineAuto(t, oneCycle(), greenBaselineFor(command), osStdin(), "add a README section", "--verify-cmd", command)

	if err != nil {
		t.Fatalf("an unpinnable command must still verify: %v\n%s", err, out)
	}
	if !strings.Contains(out, "verify-cmd has no pinned script") {
		t.Errorf("plan screen does not warn that nothing is pinned:\n%s", out)
	}
	if strings.Contains(out, "    pinned: ") {
		t.Errorf("plan screen lists a pin for a command that pinned nothing:\n%s", out)
	}
	snap := loadSnapshot(t, soleRunID(t))
	if snap.VerifyPins != nil {
		t.Errorf("verify_pins = %+v, want none", snap.VerifyPins)
	}
	wantSinkPass(t, snap.Nodes["work"])
}

// A surface with no pin set — `run`, `chat`, a resumed leg — prints neither
// a pin nor the warning (#363).
func TestNoteVerifyPins_NilSetPrintsNothing_363(t *testing.T) {
	var out strings.Builder
	noteVerifyPins(&out, nil)
	if out.Len() != 0 {
		t.Errorf("a nil set printed %q", out.String())
	}
}
