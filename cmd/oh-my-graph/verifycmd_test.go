package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// writeVerifyScript writes an executable script that exits with code and
// returns its absolute path — the stand-in for `./gradlew build` in every test
// below that needs a command the engine can really run.
//
// A written script rather than `true`/`false` from PATH: this file's own
// pre-flight resolves a program against the filesystem, so a test depending on
// where a distribution puts coreutils would be testing the distribution.
func writeVerifyScript(t *testing.T, exit int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the evidence command here is a shebang script; this pins the unix path")
	}
	path := filepath.Join(t.TempDir(), "verify.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit "+strconv.Itoa(exit)+"\n"), 0o755); err != nil {
		t.Fatalf("write verify script: %v", err)
	}
	return path
}

// soleRunID returns the one run directory the isolated OMG_HOME holds, failing
// if there is not exactly one — so an assertion about "the run" can never be
// satisfied by a run that never happened.
func soleRunID(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(runsRoot())
	if err != nil {
		t.Fatalf("read runs root: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want exactly one run directory, got %d", len(entries))
	}
	return entries[0].Name()
}

// savedSpec reads a run's saved graph.json — the artifact `run` replays and
// `resume` rebuilds from, and therefore the only place an attachment claim can
// be checked rather than believed.
func savedSpec(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, generatedSpecFileName))
	if err != nil {
		t.Fatalf("read saved spec: %v", err)
	}
	return string(raw)
}

// --- failure cases first: every one of these must cost nothing ---------------

// TestAutoFlags_UnusableVerifyFlagsAreRefusedAtParse pins the pair's parse-time
// gate. Each case is a flag combination that could never produce a working
// check, and each is refused before argv is done being read — which is what
// makes the refusal free. The timeout ceiling and the blank command are the
// coordinator's own rules (VerifyCommand.Validate); they are asserted HERE
// because the CLI is where a user meets them, and a flag that only fails after
// a planner call has already been billed is the failure ADR 0016 §2 names.
func TestAutoFlags_UnusableVerifyFlagsAreRefusedAtParse(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"timeout with no command": {
			args: []string{"goal", "--verify-timeout", "30s"},
			want: "means nothing without it",
		},
		"timeout past the ceiling": {
			args: []string{"goal", "--verify-cmd", "make", "--verify-timeout", "11m"},
			want: "ceiling",
		},
		"whitespace-only command": {
			args: []string{"goal", "--verify-cmd", "   "},
			want: "blank",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := newAutoFlags().parse(tc.args)
			if err == nil {
				t.Fatalf("parse(%v) accepted a --verify-cmd that could never run", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal %q does not say why (want %q in it)", err, tc.want)
			}
		})
	}
}

// TestAutoFlags_UnrunnableVerifyProgramIsRefusedNamingWhatWasTried is the
// pre-flight proper: a command whose program is not there, or is there without
// an execute bit, is refused before anything is planned — and the message names
// the path or the PATH that was searched, because "verify command cannot run"
// with no subject is a message a user has to reproduce by hand to act on.
func TestAutoFlags_UnrunnableVerifyProgramIsRefusedNamingWhatWasTried(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no execute bit on windows; the pre-flight deliberately stands down there")
	}
	dir := t.TempDir()
	notExecutable := filepath.Join(dir, "gradlew")
	if err := os.WriteFile(notExecutable, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatalf("write non-executable script: %v", err)
	}

	t.Run("missing file names the absolute path", func(t *testing.T) {
		missing := filepath.Join(dir, "nope", "gradlew")
		err := newAutoFlags().parse([]string{"goal", "--verify-cmd", missing + " build"})
		if err == nil {
			t.Fatal("a --verify-cmd whose program does not exist must be refused")
		}
		if !strings.Contains(err.Error(), missing) {
			t.Errorf("refusal %q does not name the path that was tried (%s)", err, missing)
		}
	})

	t.Run("present but not executable says so", func(t *testing.T) {
		err := newAutoFlags().parse([]string{"goal", "--verify-cmd", notExecutable + " build"})
		if err == nil {
			t.Fatal("a --verify-cmd whose program has no execute bit must be refused")
		}
		if !strings.Contains(err.Error(), "not executable") || !strings.Contains(err.Error(), notExecutable) {
			t.Errorf("refusal %q must name the file and the reason", err)
		}
	})

	t.Run("bare name absent from PATH names the PATH", func(t *testing.T) {
		t.Setenv("PATH", dir)
		err := newAutoFlags().parse([]string{"goal", "--verify-cmd", "definitely-not-a-real-build-tool test"})
		if err == nil {
			t.Fatal("a --verify-cmd naming a program no PATH entry holds must be refused")
		}
		if !strings.Contains(err.Error(), "definitely-not-a-real-build-tool") || !strings.Contains(err.Error(), dir) {
			t.Errorf("refusal %q must name the program and the PATH that was searched", err)
		}
	})
}

// TestAutoFlags_ShellFragmentsSkipTheExecutableCheck is the pre-flight's
// deliberate blind spot, asserted so nobody later "fixes" it into a refusal.
//
// `sh -c` runs the command, so a fragment carrying a pipeline, a conditional or
// an assignment is the shell's to resolve — and re-deciding it here would be a
// second, worse shell. The concrete regression this guards: `cd sub && make`
// starts with `cd`, which is a builtin on Linux and a real file on macOS, so a
// naive first-token check refuses a working command on one platform only.
func TestAutoFlags_ShellFragmentsSkipTheExecutableCheck(t *testing.T) {
	for _, command := range []string{
		"cd sub && definitely-not-a-real-build-tool",
		"definitely-not-a-real-build-tool | tee log",
		"CI=1 definitely-not-a-real-build-tool",
	} {
		if err := newAutoFlags().parse([]string{"goal", "--verify-cmd", command}); err != nil {
			t.Errorf("parse(--verify-cmd %q) = %v, want it accepted — the shell decides, not this pre-flight", command, err)
		}
	}
}

// TestRunAutoWith_UnrunnableVerifyCmdBuysNoPlannerCall is the whole reason the
// check above sits at parse: an unusable command must not cost a paid plan
// (ADR 0016 §2). Asserted through the real `auto` argv path with the runner
// seam counting invocations, because "checked early" is a claim about ORDER
// that a unit test of the checker cannot make.
func TestRunAutoWith_UnrunnableVerifyCmdBuysNoPlannerCall(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
	})

	missing := filepath.Join(t.TempDir(), "gradlew")
	var err error
	captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--verify-cmd", missing,
			"--no-agent-mapping", "--no-skill-mapping"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err == nil {
		t.Fatal("auto accepted a --verify-cmd that cannot run")
	}
	if n := len(fake.Invocations()); n != 0 {
		t.Errorf("the refusal cost %d call(s) — a planner call is billed whether or not the plan is usable", n)
	}
}

// TestRunAutoWith_AFailedSinkCheckFailsTheRun is #119's reported failure,
// inverted: the node replies and would have passed on its own predicate, the
// engine runs the user's command anyway, the command exits non-zero, and the
// run FAILS. Nothing about the node's narration can rescue it, which is the
// point — the evidence is the engine's, not the model's.
func TestRunAutoWith_AFailedSinkCheckFailsTheRun(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 1)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--verify-cmd", script,
			"--no-agent-mapping", "--no-skill-mapping"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err == nil {
		t.Fatal("a sink whose evidence command exited non-zero must fail the run")
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("the ledger must show the failed sink:\n%s", out)
	}
	if strings.Contains(out, "PASS (verified)") {
		t.Errorf("a failed check printed as verified:\n%s", out)
	}
}

// --- success cases -----------------------------------------------------------

// TestRunAutoWith_PlanOnlyShowsWhatTheSinksWillRun is the flag pair's
// disclosure. --plan-only exists so a user can see a plan before paying for its
// execution, and after ADR 0016 the plan includes a shell command the ENGINE
// will run outside every ceiling layer the same screen describes. Showing the
// topology and hiding that command would be showing the cheap half.
//
// The saved spec is checked too, and separately: the printout is what the user
// reads, the graph.json is what a later `run` replays.
func TestRunAutoWith_PlanOnlyShowsWhatTheSinksWillRun(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
	})

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--plan-only", "--verify-cmd", script,
			"--verify-timeout", "90s", "--no-agent-mapping", "--no-skill-mapping"},
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("--plan-only with a runnable --verify-cmd returned error: %v", err)
	}
	if n := len(fake.Invocations()); n != 1 {
		t.Fatalf("--plan-only made %d calls, want exactly the one planner call", n)
	}
	for _, want := range []string{
		"build evidence",   // it is named as what it is
		script,             // the command itself, verbatim
		"work",             // on the sink node it will run at
		"1m30s",            // under the bound the user asked for, resolved
		"one at a time",    // and serialized, which changes what a run costs
		"ENGINE runs this", // by whom, since that is the whole claim
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan-only output should contain %q:\n%s", want, out)
		}
	}
	// The advice line is for the zero-config path only: printing "nothing will
	// compile your code" beside the command that compiles it would teach the
	// user to ignore the line.
	if strings.Contains(out, "No build verification configured") {
		t.Errorf("the zero-config advice printed although a command was supplied:\n%s", out)
	}

	spec := savedSpec(t, solePlanDir(t))
	if !strings.Contains(spec, script) || !strings.Contains(spec, "verify") {
		t.Errorf("the saved spec does not carry the check the printout promised:\n%s", spec)
	}
}

// TestRunAutoWith_VerifiedSinkIsRunByTheEngine is the flag end to end: argv →
// coordinator → sink attachment → the verify seam → the ledger's provenance
// qualifier. `work` declares no predicate of its own, so before ADR 0016 its
// best row was `exit-only`; with the user's command attached the engine
// observed state outside the model's narration and the row says `verified`.
func TestRunAutoWith_VerifiedSinkIsRunByTheEngine(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--verify-cmd", script,
			"--no-agent-mapping", "--no-skill-mapping"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("a run whose evidence command exits 0 must pass: %v", err)
	}
	if !strings.Contains(out, "PASS (verified)") {
		t.Errorf("the sink's row must say the engine gathered the evidence:\n%s", out)
	}
}

// TestRunAutoWith_ZeroConfigSaysWhatItDidNotCheck is ADR 0016 §3: a run with no
// --verify-cmd is not silently weaker, it says so, before the planner call
// rather than beside a finished ledger — and it names the one flag that would
// change it. Nothing is attached, and the printed line is prose: detection
// informs, the human authorizes.
func TestRunAutoWith_ZeroConfigSaysWhatItDidNotCheck(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
	})

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--plan-only",
			"--no-agent-mapping", "--no-skill-mapping"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("a zero-config plan-only returned error: %v", err)
	}
	for _, want := range []string{
		"No build verification configured",
		"--verify-cmd",
		"no node's PASS carries build evidence",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("zero-config output should contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "build evidence (--verify-cmd)") {
		t.Errorf("an attachment was disclosed for a run that supplied no command:\n%s", out)
	}
	if spec := savedSpec(t, solePlanDir(t)); strings.Contains(spec, "verify") {
		t.Errorf("the saved spec carries a verification nobody asked for:\n%s", spec)
	}
}

// TestPlanAndExecute_EveryGoalCycleSinkCarriesTheCommand answers the
// --max-cycles interaction, which is not obvious from either flag alone: a
// cycle does not inherit cycle 1's graph, it plans a NEW one from the previous
// cycle's assessment. The command is held by the coordinator the loop
// re-enters, not by a plan, so every cycle's sinks carry it and every cycle's
// run is gated on it. The failure this pins is a build command that quietly
// stops applying at cycle 2 — the cycles that exist precisely because cycle 1
// did not finish the job.
func TestPlanAndExecute_EveryGoalCycleSinkCarriesTheCommand(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1":   {SessionID: "s-work-1", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-1": {Result: cycleAssessNotMet, TotalCostUSD: 0.02},
		"plan-2":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-2":   {SessionID: "s-work-2", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-2": {Result: cycleAssessMet, TotalCostUSD: 0.02},
	})

	var out strings.Builder
	var err error
	captureStdout(t, func() {
		coord := coordinator.New(fake, coordinator.WithVerifyCommand(coordinator.VerifyCommand{Command: script}))
		err = planAndExecute(context.Background(), &out, coord, fake, commonRunFlags{inputs: inputFlag{}},
			"add a README section", goalCycleOptions{maxCycles: 2}, false, nil, nil)
	})
	if err != nil {
		t.Fatalf("a met goal on verified runs must exit clean: %v", err)
	}

	entries, readErr := os.ReadDir(runsRoot())
	if readErr != nil {
		t.Fatalf("read runs root: %v", readErr)
	}
	if len(entries) != 2 {
		t.Fatalf("want one run directory per cycle, got %d", len(entries))
	}
	for _, entry := range entries {
		if spec := savedSpec(t, filepath.Join(runsRoot(), entry.Name())); !strings.Contains(spec, script) {
			t.Errorf("cycle %s planned a graph with no build evidence on it:\n%s", entry.Name(), spec)
		}
	}
}

// TestResume_RefusesAnAutoRunThatCarriedItsOwnVerifyCommand is the residual ADR
// 0016 §4 records, now reachable from a command line rather than only from a
// hand-edited snapshot.
//
// The attached command IS legitimately in this run's graph.json — trusted code
// put it there from a string the user typed. But `resume` cannot tell that
// snapshot from a tampered one: it never re-runs plan validation, and a
// verification is engine-run shell outside every ceiling layer. So it refuses
// every snapshot-borne command on an auto graph, and since `resume` registers
// no --verify-cmd of its own (mechanism (i)'s re-supply half, unshipped), the
// refusal is terminal: an auto run carrying build evidence cannot be resumed.
//
// That is a real cost of this flag and it is pinned here rather than left to be
// discovered, so the day `resume` learns the flag this test is what changes.
func TestResume_RefusesAnAutoRunThatCarriedItsOwnVerifyCommand(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 1)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})

	var runErr error
	captureStdout(t, func() {
		runErr = runAutoWith([]string{"add a README section", "--verify-cmd", script,
			"--no-agent-mapping", "--no-skill-mapping"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if runErr == nil {
		t.Fatal("the failing evidence command must have failed the run, or there is nothing to resume")
	}

	runID := soleRunID(t)
	var resumeErr error
	captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), fake, nil)
	})
	var snapErr *coordinator.SnapshotVerifyError
	if !errors.As(resumeErr, &snapErr) {
		t.Fatalf("resume err = %v (%T), want the *SnapshotVerifyError — a resumed leg takes no verification from disk", resumeErr, resumeErr)
	}
	if len(snapErr.NodeIDs) != 1 || snapErr.NodeIDs[0] != "work" {
		t.Errorf("refusal names %v, want exactly [work] — the sink the command was attached to", snapErr.NodeIDs)
	}
}

// TestNoteVerifyAttachments_SaysNothingWithoutOne keeps the disclosure honest
// in the direction nothing else covers: silence must mean "no command", never
// "a command nobody printed".
func TestNoteVerifyAttachments_SaysNothingWithoutOne(t *testing.T) {
	var out strings.Builder
	noteVerifyAttachments(&out, nil)
	if out.Len() != 0 {
		t.Errorf("an empty attachment set printed %q", out.String())
	}
	noteVerifyAttachments(&out, []coordinator.VerifyAttachment{
		{NodeID: "sink", Command: "make", Timeout: 10 * time.Minute},
	})
	for _, want := range []string{"sink", "make", "10m0s"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("disclosure omits %q: %s", want, out.String())
		}
	}
}

// TestSavedSpec_IsStillValidJSON guards the one silent failure the attachment
// could introduce into an artifact other tools read: graph.json is re-encoded
// after the mutation, and a spec that no longer parses would break `run
// <graph.json>` and every consumer of a run directory long after the run that
// wrote it.
func TestSavedSpec_IsStillValidJSON(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
	})
	captureStdout(t, func() {
		if err := runAutoWith([]string{"goal", "--plan-only", "--verify-cmd", script,
			"--no-agent-mapping", "--no-skill-mapping"}, fake, browser.NewFakeOpener(), os.Stdout); err != nil {
			t.Errorf("plan-only: %v", err)
		}
	})
	if !json.Valid([]byte(savedSpec(t, solePlanDir(t)))) {
		t.Error("the verify-attached spec is not valid JSON, so `run graph.json` would fail on it")
	}
}
