package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// ADR 0030 — an unverified `auto` run is a choice, not a default.
//
// The defect these tests pin is structural rather than statistical: a planned
// node cannot carry success_check.verify (the planner's reply is untrusted
// input) and cannot declare a build tool, so WITHOUT --verify-cmd an auto run
// has by construction no engine-run evidence at all. #119 is what that costs —
// a verify node holding `Bash(git *)` checked that a branch existed, replied
// PASS in 17 seconds after the node before it spent $11, and the real build then
// failed on a compile error.

// inBuildDir writes each named marker into a fresh temp dir and makes it the
// invocation directory, returning its path. The gate scans "." — the tree the
// planned nodes and the evidence command would both run in — so a test about
// what a repository shape does has to BE in that shape.
func inBuildDir(t *testing.T, markers ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range markers {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	t.Chdir(dir)
	return dir
}

// recordedBuildEvidence is the block the run left in its snapshot — the whole
// point of the field being written on every launch, and the only way to tell a
// chosen absence from an accidental one after the fact.
func recordedBuildEvidence(t *testing.T, runID string) *runstate.BuildEvidence {
	t.Helper()
	snap, err := runstate.Load(filepath.Join(runDirFor(runID), runstate.SnapshotFileName))
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	return snap.BuildEvidence
}

// --- 1. a build signal met by silence is refused, before anything is spent ----

// TestRunAutoWith_RefusesABuildBearingDirectoryWithNoEvidenceCommand is the
// decision itself. Asserting only "it refused" would pass on a bare exit 1, so
// the message is asserted too: what was detected (by ecosystem AND file) and
// both exits, because a refusal with one exit is a wall and a refusal with two
// is a question.
func TestRunAutoWith_RefusesABuildBearingDirectoryWithNoEvidenceCommand(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t, "gradlew")
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
	})

	var err error
	captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section",
			"--no-agent-mapping", "--no-skill-activation"}, fake, browser.NewFakeOpener(), os.Stdout)
	})

	var refusal *coordinator.MissingBuildEvidenceError
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v (%T), want the *MissingBuildEvidenceError", err, err)
	}
	var text strings.Builder
	refusal.Print(&text)
	for _, want := range []string{"a Gradle project (gradlew)", "--verify-cmd './gradlew build'", "--accept-no-build-evidence"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("the refusal does not name %q:\n%s", want, text.String())
		}
	}
	// Refused BEFORE the planner call, which is half of why the gate sits where
	// it does: a refusal that cost a planner call would be a worse version of the
	// notice it replaces.
	if n := len(fake.Invocations()); n != 0 {
		t.Errorf("the refused invocation made %d call(s); a refusal must spend nothing", n)
	}
	if entries, readErr := os.ReadDir(runsRoot()); readErr == nil && len(entries) != 0 {
		t.Errorf("the refused invocation left %d run director(ies); nothing ran, so there is no run", len(entries))
	}
}

// --- 2. a supplied command proceeds unchanged, and the detection is recorded --

// TestRunAutoWith_SuppliedCommandProceedsAndRecordsTheDetection pins both
// halves. The behaviour half is that --verify-cmd is untouched by ADR 0030:
// same attachment, same disclosure. The recording half is what makes §8(a)'s
// denominator exist at all — a field written only for the declared stratum
// would leave "how many directories raised a signal" exactly as unknowable
// after shipping as it was before.
func TestRunAutoWith_SuppliedCommandProceedsAndRecordsTheDetection(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	inBuildDir(t, "go.mod")
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--verify-cmd", script,
			"--no-agent-mapping", "--no-skill-activation"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("a run carrying build evidence must not be gated: %v", err)
	}
	if !strings.Contains(out, "build evidence (--verify-cmd)") {
		t.Errorf("the attachment was not disclosed with the plan:\n%s", out)
	}
	if strings.Contains(out, "build evidence: NONE") {
		t.Errorf("a run that attached a command was reported as carrying none:\n%s", out)
	}

	evidence := recordedBuildEvidence(t, soleRunID(t))
	if evidence == nil {
		t.Fatal("an auto launch recorded no build_evidence block at all")
	}
	if evidence.Answer != "attached" {
		t.Errorf("answer = %q, want %q", evidence.Answer, "attached")
	}
	if evidence.DeclaredBy != "" {
		t.Errorf("declared_by = %q, want empty — nobody declared anything here", evidence.DeclaredBy)
	}
	if len(evidence.Signals) != 1 || evidence.Signals[0] != "go.mod" {
		t.Errorf("signals = %v, want [go.mod] — the denominator counts the attached rows too", evidence.Signals)
	}
}

// --- 3. the negative control: no build signal is not a gate -------------------

// TestRunAutoWith_NoBuildSignalIsNotAGate is the test that would otherwise be
// forgotten, in both halves. Without the first, the gate could widen to "refuse
// always" and every other test in this file would still pass. Without the
// second, the greenfield run — `auto "scaffold a new Go service"` in an empty
// directory, the highest-risk unverified run there is — stays invisible and
// §8(a) loses the stratum that would size it.
func TestRunAutoWith_NoBuildSignalIsNotAGate(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t) // no markers: a greenfield directory
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"scaffold a new Go service",
			"--no-agent-mapping", "--no-skill-activation"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("a directory with no build system has nothing for an evidence command to be evidence about: %v", err)
	}
	if !strings.Contains(out, "Detected no build signal in this directory") {
		t.Errorf("the un-signalled case's notice changed; it is unchanged by ADR 0030:\n%s", out)
	}

	evidence := recordedBuildEvidence(t, soleRunID(t))
	if evidence == nil {
		t.Fatal("a greenfield launch recorded no build_evidence block, so its class cannot be counted")
	}
	if evidence.Answer != "none-detected" {
		t.Errorf("answer = %q, want %q", evidence.Answer, "none-detected")
	}
	if len(evidence.Signals) != 0 {
		t.Errorf("signals = %v, want none", evidence.Signals)
	}
}

// TestRunAutoWith_TheFlagIsInertWhereNothingWasDetected — a script that always
// passes the opt-out must not break in a directory with no build system, and the
// run it produces is NOT a declaration: the flag answered a question nobody put,
// and filing it as one would inflate exactly the stratum the measurement exists
// to count.
func TestRunAutoWith_TheFlagIsInertWhereNothingWasDetected(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})

	var err error
	captureStdout(t, func() {
		err = runAutoWith([]string{"scaffold a new Go service", "--accept-no-build-evidence",
			"--no-agent-mapping", "--no-skill-activation"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("the opt-out must be accepted and inert where nothing was detected: %v", err)
	}
	if answer := recordedBuildEvidence(t, soleRunID(t)).Answer; answer != "none-detected" {
		t.Errorf("answer = %q, want %q — a flag that answered no question is not a declaration", answer, "none-detected")
	}
}

// --- 4. the opt-out proceeds, and the run says so ----------------------------

// TestRunAutoWith_DeclaredAbsenceProceedsAndIsRecorded is the exit the refusal
// offers, and the receipt it promises. Both places a reader meets the run are
// asserted: the plan screen it is printed on, and the state.json a reader of a
// FINISHED run has. An opt-out with no record would convert an invisible default
// into an invisible habit, which is not the trade this ADR makes.
func TestRunAutoWith_DeclaredAbsenceProceedsAndIsRecorded(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t, "package.json")
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--accept-no-build-evidence",
			"--no-agent-mapping", "--no-skill-activation"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("a declared run must proceed: %v", err)
	}
	for _, want := range []string{
		"You said so with --accept-no-build-evidence",
		"build evidence: NONE",
		"detected in this directory: package.json",
		"this run's state.json records it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the declared run does not print %q:\n%s", want, out)
		}
	}

	evidence := recordedBuildEvidence(t, soleRunID(t))
	if evidence == nil {
		t.Fatal("the declared absence was not recorded, so a later reader cannot tell it from an accident")
	}
	if evidence.Answer != "declared" {
		t.Errorf("answer = %q, want %q", evidence.Answer, "declared")
	}
	if evidence.DeclaredBy != "--accept-no-build-evidence" {
		t.Errorf("declared_by = %q, want the flag's own spelling", evidence.DeclaredBy)
	}
	if len(evidence.Signals) != 1 || evidence.Signals[0] != "package.json" {
		t.Errorf("signals = %v, want [package.json] — what the human was told when they answered", evidence.Signals)
	}
}

// TestRunGraphWith_HandWrittenGraphIsNeitherGatedNorRecorded is the `run` row of
// ADR 0030 §2.6, and it needs a test because the plumbing runs through
// commonRunFlags, which `run` shares with `auto`: a field set on the wrong side
// of that struct would gate a hand-written graph or file it under an answer
// nobody was asked for. Both are wrong — a hand-written graph carries its
// author's own success_check.verify and is a reviewed artifact — and the four
// strata are meant to sum to the auto-mode launches and nothing else.
func TestRunGraphWith_HandWrittenGraphIsNeitherGatedNorRecorded(t *testing.T) {
	isolateRunHome(t)
	dir := inBuildDir(t, "gradlew", "Makefile")
	path := filepath.Join(dir, "graph.yaml")
	if err := os.WriteFile(path, []byte("name: hand-written\nnodes:\n  - { id: dev, prompt: do the work }\n"), 0o644); err != nil {
		t.Fatalf("write graph: %v", err)
	}
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"do the work": {SessionID: "s-dev", Result: "PASS", ExitCode: 0},
	})

	var err error
	out := captureStdout(t, func() {
		err = runGraphWith([]string{path}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("a hand-written graph must not be gated on build evidence: %v", err)
	}
	if strings.Contains(out, "build evidence") {
		t.Errorf("`run` was told about a question it never asks:\n%s", out)
	}
	if evidence := recordedBuildEvidence(t, soleRunID(t)); evidence != nil {
		t.Errorf("a hand-written run recorded %+v; it never asks the question, so it records no answer", evidence)
	}
}

// --- 5. the preview and the interactive surface ------------------------------

// TestRunAutoWith_PlanOnlyRefusesIdenticallyToTheRunItPreviews — a preview that
// refuses differently from the run it previews is its own defect. It falls out
// of the gate's placement rather than a special case, and it SAVES the user
// money in the refused case: today they would pay for a plan they then have to
// re-request with a flag.
func TestRunAutoWith_PlanOnlyRefusesIdenticallyToTheRunItPreviews(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t, "Cargo.toml")
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
	})

	var previewErr error
	captureStdout(t, func() {
		previewErr = runAutoWith([]string{"add a README section", "--plan-only",
			"--no-agent-mapping", "--no-skill-activation"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	var preview *coordinator.MissingBuildEvidenceError
	if !errors.As(previewErr, &preview) {
		t.Fatalf("--plan-only err = %v (%T), want the same refusal the run gets", previewErr, previewErr)
	}

	var runErr error
	captureStdout(t, func() {
		runErr = runAutoWith([]string{"add a README section",
			"--no-agent-mapping", "--no-skill-activation"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	var full *coordinator.MissingBuildEvidenceError
	if !errors.As(runErr, &full) {
		t.Fatalf("run err = %v (%T), want the refusal", runErr, runErr)
	}

	var previewText, fullText strings.Builder
	preview.Print(&previewText)
	full.Print(&fullText)
	if previewText.String() != fullText.String() {
		t.Errorf("the preview refuses differently from the run it previews:\n--- preview ---\n%s\n--- run ---\n%s", previewText.String(), fullText.String())
	}
	if exitCodeForError(previewErr) != exitCodeForError(runErr) {
		t.Errorf("preview exits %d, run exits %d — the same refusal must exit the same way",
			exitCodeForError(previewErr), exitCodeForError(runErr))
	}
	if n := len(fake.Invocations()); n != 0 {
		t.Errorf("the two refusals bought %d planner call(s) between them, want 0", n)
	}
}

// TestRunChatRuntime_DoesNotRefuseInABuildBearingDirectory — `chat` registers no
// verification flags, so a refusal there could only name a flag `chat` rejects,
// which is the dead end #198 was. It asks the question and answers it with a
// disclosure instead. Stdin is closed, so the loop ends at once and nothing is
// ever spawned; what is under test is the gate the launch passes through first.
func TestRunChatRuntime_DoesNotRefuseInABuildBearingDirectory(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t, "gradlew", "package.json")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	_ = w.Close()
	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin; _ = r.Close() })

	var chatErr error
	captureStdout(t, func() {
		chatErr = runChatRuntime(runner.RuntimeClaude, []string{"--no-agent-mapping", "--no-skill-activation"})
	})
	var refusal *coordinator.MissingBuildEvidenceError
	if errors.As(chatErr, &refusal) {
		t.Fatal("chat refused for want of build evidence, naming flags it does not register (#198)")
	}
	if chatErr != nil {
		t.Fatalf("chat returned %v", chatErr)
	}
}

// TestRunChatWith_TheLaunchItselfDeclaresAChatConfirmDisclosure closes the half
// of chat's wiring the two tests around it leave open. The one above asserts
// only that no refusal came back; the one below constructs the outcome itself
// and hands it to chatLoop. Between them sits the single line of policy that
// decides WHICH declaration this surface makes — and if it passed
// DeclaredByFlag, both of them would still pass while every chat run on the
// machine filed itself under `declared`, merging the two strata ADR 0030 §2.6
// and §8(a) spend two paragraphs keeping apart.
//
// So this drives the production launch path, through a real graph turn, to the
// snapshot it leaves behind.
func TestRunChatWith_TheLaunchItselfDeclaresAChatConfirmDisclosure(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t, "gradlew")
	fake := newChatFake(map[string]runner.NodeOutcome{
		routerKey:   {Result: `{"mode":"graph","goal":"add a README section"}`},
		autoPlanKey: {Result: cycleSpec, TotalCostUSD: 0.0417},
		"work":      {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})

	var out strings.Builder
	var chatErr error
	captureStdout(t, func() {
		chatErr = runChatWith(runner.RuntimeClaude, []string{"--no-agent-mapping", "--no-skill-activation"},
			strings.NewReader("add a README section\ny\n"), &out, fake)
	})
	if chatErr != nil {
		t.Fatalf("chat returned %v", chatErr)
	}

	recorded := recordedBuildEvidence(t, soleRunID(t))
	if recorded == nil {
		t.Fatal("a chat graph turn launched by runChatWith recorded no build_evidence block")
	}
	if recorded.Answer != "disclosed" {
		t.Errorf("answer = %q, want %q — chat's launch declared something other than its own keystroke", recorded.Answer, "disclosed")
	}
	if recorded.DeclaredBy != "chat-confirm" {
		t.Errorf("declared_by = %q, want %q — chat filed its run under a flag it does not register", recorded.DeclaredBy, "chat-confirm")
	}
}

// TestChatLoop_StatesTheAbsenceOnTheScreenItsConfirmGates is chat's whole
// answer, and it is filed as what it is. One `y` covers two questions — run this
// plan, and accept that it proves nothing — where `auto`'s operator answers the
// second one separately. So the run records `disclosed`, DISTINCT from test 4's
// `declared`: merging the two later would take this assertion red, which is the
// structural help a future reader of the strata needs and would not otherwise
// have.
func TestChatLoop_StatesTheAbsenceOnTheScreenItsConfirmGates(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t, "gradlew")
	fake := newChatFake(map[string]runner.NodeOutcome{
		routerKey:   {Result: `{"mode":"graph","goal":"add a README section"}`},
		autoPlanKey: {Result: cycleSpec, TotalCostUSD: 0.0417},
		"work":      {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})
	evidence, err := coordinator.RequireBuildEvidence(coordinator.VerifyCommand{}, coordinator.DeclaredByChatConfirm,
		coordinator.DetectBuildSignals("."))
	if err != nil {
		t.Fatalf("chat must never be refused: %v", err)
	}

	var out strings.Builder
	captureStdout(t, func() {
		_ = chatLoop(context.Background(), strings.NewReader("add a README section\ny\n"), &out,
			coordinator.New(fake), fake, commonRunFlags{inputs: inputFlag{}, buildEvidence: &evidence})
	})

	for _, want := range []string{
		"build evidence: NONE",
		"detected in this directory: gradlew",
		"approving this plan accepts that",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the screen chat's [y/N] gates does not state %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "--accept-no-build-evidence") {
		t.Errorf("chat's screen names a flag chat does not register (#198):\n%s", out.String())
	}

	recorded := recordedBuildEvidence(t, soleRunID(t))
	if recorded == nil {
		t.Fatal("a chat graph turn recorded no build_evidence block")
	}
	if recorded.Answer != "disclosed" {
		t.Errorf("answer = %q, want %q — a keystroke covering two questions is not a declaration", recorded.Answer, "disclosed")
	}
	if recorded.DeclaredBy != "chat-confirm" {
		t.Errorf("declared_by = %q, want %q", recorded.DeclaredBy, "chat-confirm")
	}
}

// --- 6, 7. the channel, the prefix and the code ------------------------------

// TestMainExitCode_RefusalPrintsItselfToStdoutAndExitsThree is about the two
// things "return an error" would have got wrong, and neither is the error's own
// behaviour — both belong to mainExitCode.
//
// An ordinary error is printed as `oh-my-graph: %v` on STDERR, which would
// double-prefix the refusal's first sentence and put nineteen indented lines on
// the channel the notice it replaces does not use. And exit 1 would make a
// refusal indistinguishable from the failing build the operator is trying to
// catch, so a script could not tell "add a flag" from "page someone".
func TestMainExitCode_RefusalPrintsItselfToStdoutAndExitsThree(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t, "pom.xml")

	var code int
	var stderr string
	stdout := captureStdout(t, func() {
		var captureErr error
		stderr, captureErr = captureStderr(t, func() error {
			code = mainExitCode([]string{"auto", "add a README section"})
			return nil
		})
		if captureErr != nil {
			t.Errorf("capture stderr: %v", captureErr)
		}
	})

	if code != 3 {
		t.Errorf("exit code = %d, want 3 — a refusal is neither a failed run (1) nor a paused one (2)", code)
	}
	if strings.Contains(stdout, "oh-my-graph: auto:") {
		t.Errorf("the refusal was printed through the error prefix, doubling its own:\n%s", stdout)
	}
	if strings.Contains(stderr, "build system") {
		t.Errorf("the refusal went to stderr; the notice it replaces goes to stdout:\n%s", stderr)
	}
	for _, want := range []string{
		"this directory has a build system",
		"Detected a Maven project (pom.xml)",
		"--verify-cmd 'mvn -q verify'",
		"--accept-no-build-evidence",
		"Nothing has been spent",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not carry %q, so the whole refusal did not arrive there:\n%s", want, stdout)
		}
	}
}

// TestExitCodeForError_RefusalIsItsOwnCode pins 3 beside the codes it must not
// collide with, in the function that owns the mapping. ADR 0023 §2.6's
// exit-code/run-status agreement is undisturbed: a refused invocation creates no
// run directory, so it is outside that assertion rather than a new case in it.
func TestExitCodeForError_RefusalIsItsOwnCode(t *testing.T) {
	cases := map[string]struct {
		err  error
		want int
	}{
		"clean":   {err: nil, want: 0},
		"failed":  {err: errors.New("the run failed"), want: 1},
		"refused": {err: &coordinator.MissingBuildEvidenceError{Signals: coordinator.DetectBuildSignals(".")}, want: 3},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := exitCodeForError(tc.err); got != tc.want {
				t.Errorf("exitCodeForError(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// --- 8. one scan, one directory ----------------------------------------------

// TestAnswerBuildEvidence_GateAndAdviceScanTheSameDirectory is the failure the
// rejected placement (inside autoFlags.parse) would have made possible AND
// untested: two DetectBuildSignals sweeps at two call sites that must agree on
// their `dir`, with nothing pinning the two arguments together — the message
// tests pin what is SAID, not where it was looked for, so a gate scanning one
// directory while the advice line scanned another would pass all of them.
//
// The fixture makes the two directories disagree on purpose: the process cwd
// raises a Gradle signal, the scanned directory raises a Cargo one. Every
// consumer must describe Cargo.
func TestAnswerBuildEvidence_GateAndAdviceScanTheSameDirectory(t *testing.T) {
	inBuildDir(t, "gradlew")
	scanned := t.TempDir()
	if err := os.WriteFile(filepath.Join(scanned, "Cargo.toml"), nil, 0o644); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	var advice strings.Builder
	outcome, err := answerBuildEvidence(&advice, coordinator.VerifyCommand{}, coordinator.DeclaredByFlag, scanned)
	if err != nil {
		t.Fatalf("a declared absence must proceed: %v", err)
	}
	if got := outcome.SignalFiles(); len(got) != 1 || got[0] != "Cargo.toml" {
		t.Errorf("recorded signals = %v, want [Cargo.toml] — the record must describe the scanned directory", got)
	}
	if !strings.Contains(advice.String(), "cargo test") {
		t.Errorf("the advice line describes a different directory from the record:\n%s", advice.String())
	}
	if strings.Contains(advice.String(), "gradlew") {
		t.Errorf("the advice line fell back to the process directory:\n%s", advice.String())
	}

	if _, err := answerBuildEvidence(io.Discard, coordinator.VerifyCommand{}, coordinator.NoDeclaration, scanned); err != nil {
		var refusal *coordinator.MissingBuildEvidenceError
		if !errors.As(err, &refusal) {
			t.Fatalf("err = %v (%T), want the refusal", err, err)
		}
		if len(refusal.Signals) != 1 || refusal.Signals[0].File != "Cargo.toml" {
			t.Errorf("the refusal names %+v, want the scanned directory's Cargo.toml", refusal.Signals)
		}
	} else {
		t.Fatal("a signal met by silence was not refused")
	}
}

// --- the two that fall out of the flag pair and the message ------------------

// TestAutoFlags_DeclaringAnAbsenceWhileSupplyingItIsRefused — the operator would
// be declaring an absence and supplying the thing whose absence they declared.
// Refusing beats picking a winner: either winner silently discards something
// they typed. A pure flag-pair check, with no directory involved.
func TestAutoFlags_DeclaringAnAbsenceWhileSupplyingItIsRefused(t *testing.T) {
	err := newAutoFlags().parse([]string{"a goal", "--verify-cmd", "make", "--accept-no-build-evidence"})
	if err == nil {
		t.Fatal("parse accepted a run that both declares no build evidence and supplies some")
	}
	for _, want := range []string{"--accept-no-build-evidence", "--verify-cmd"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so it does not say which two flags collided: %v", want, err)
		}
	}
}

// TestMissingBuildEvidenceRefusal_NamesOnlyFlagsAutoRegisters is #198's rule
// applied to the new refusal, and it is deliberately a test about a STRING.
// A message that sends someone to a flag the tool rejects costs more than
// silence, because the next message is not believed either.
func TestMissingBuildEvidenceRefusal_NamesOnlyFlagsAutoRegisters(t *testing.T) {
	registered := registeredFlags(newAutoFlags().set)
	var text strings.Builder
	(&coordinator.MissingBuildEvidenceError{Signals: []coordinator.BuildSignal{
		{File: "gradlew", Ecosystem: "a Gradle project", SuggestedCommand: "./gradlew build"},
	}}).Print(&text)

	named := usageFlagPattern.FindAllStringSubmatch(text.String(), -1)
	if len(named) == 0 {
		t.Fatalf("the refusal names no flag at all, so it tells the user nothing they can do:\n%s", text.String())
	}
	for _, match := range named {
		if !registered[match[1]] {
			t.Errorf("the refusal tells the user to re-run with `--%s`, which `auto` does not register — "+
				"following it gets `flag provided but not defined` (#198):\n%s", match[1], text.String())
		}
	}
}

// --- the seams between legs, surfaces and cycles ------------------------------

// TestResume_CarriesTheDeclarationIntoTheSecondLeg is ADR 0030 §2.6's `resume`
// row, asserted rather than assumed. That row declines to re-ask the question,
// and its whole justification is one sentence — "the choice was made once, and
// the snapshot the resume loads records it" — which is a claim about the
// snapshot AFTER the resume, not before it.
//
// The failure it guards is silent and total, not partial. SnapshotRecorder
// writes the WHOLE snapshot from its base on every RecordNode, so a resumed leg
// whose base names the fields one by one and omits build_evidence does not fail
// to add one: the first node that settles ERASES the block the first leg wrote.
// What is left is a finished run in which a chosen absence and an accidental one
// look identical again — and, worse for §8(a), a run that has silently left all
// four strata, biasing the denominator by "did this run pause", which is exactly
// the interactive class a human declares over.
func TestResume_CarriesTheDeclarationIntoTheSecondLeg(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t, "package.json")
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
		"work-1": {ExitCode: 1, FailureCause: limitCauseMsg, SessionLimited: true},
		// The retry leg relaunches the same node; this is the leg that rewrites
		// state.json from the resumed base.
		"work-2": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})

	var err error
	captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--accept-no-build-evidence",
			"--no-agent-mapping", "--no-skill-activation"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	var limited *schedule.LimitPausedError
	if !errors.As(err, &limited) {
		t.Fatalf("the first leg should pause on the session limit, got %T: %v", err, err)
	}
	runID := soleRunID(t)
	before := recordedBuildEvidence(t, runID)
	if before == nil || before.Answer != "declared" {
		t.Fatalf("the paused leg recorded %+v, want the declaration this test then resumes over", before)
	}

	var resumeErr error
	captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), fake, nil)
	})
	if resumeErr != nil {
		t.Fatalf("the retry leg should finish the run cleanly, got: %v", resumeErr)
	}

	after := recordedBuildEvidence(t, runID)
	if after == nil {
		t.Fatal("the resumed leg erased build_evidence; ADR 0030 §2.6 does not re-gate a resume BECAUSE the snapshot records it")
	}
	if after.Answer != before.Answer || after.DeclaredBy != before.DeclaredBy {
		t.Errorf("after resume = %+v, want the first leg's %+v — a declaration is a fact about the run, not about one leg", after, before)
	}
	if len(after.Signals) != 1 || after.Signals[0] != "package.json" {
		t.Errorf("signals after resume = %v, want [package.json] — what the human was told when they answered", after.Signals)
	}
}

// TestRunAutoWith_EveryCycleOfAGoalLoopRecordsTheOneAnswer — a --max-cycles loop
// mints a fresh run id and a fresh recorder per cycle, so "the question is asked
// once per invocation" (§3.5) is only true of the RECORD if every cycle's
// snapshot carries the answer the invocation gave. A cycle that recorded nothing
// would drop out of §8(a) exactly as a resumed leg would, and a cycle that
// re-asked would let a build system a previous cycle WROTE gate the run — the
// bootstrapping shape §3.5 keeps out.
func TestRunAutoWith_EveryCycleOfAGoalLoopRecordsTheOneAnswer(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t, "Cargo.toml")
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1":   {SessionID: "s-work-1", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-1": {Result: cycleAssessNotMet, TotalCostUSD: 0.02},
		"plan-2":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-2":   {SessionID: "s-work-2", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-2": {Result: cycleAssessMet, TotalCostUSD: 0.02},
	})

	var err error
	captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--accept-no-build-evidence", "--max-cycles", "2",
			"--no-agent-mapping", "--no-skill-activation"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("a declared goal loop must run to its met verdict: %v", err)
	}

	snaps := goalSnapshots(t)
	if len(snaps) != 2 {
		t.Fatalf("%d run directories, want one per cycle (2)", len(snaps))
	}
	for i, snap := range snaps {
		cycle := i + 1
		if snap.BuildEvidence == nil {
			t.Fatalf("cycle %d recorded no build_evidence, so it is invisible to §8(a)", cycle)
		}
		if snap.BuildEvidence.Answer != "declared" || snap.BuildEvidence.DeclaredBy != "--accept-no-build-evidence" {
			t.Errorf("cycle %d recorded %+v, want the invocation's own declaration", cycle, snap.BuildEvidence)
		}
		if len(snap.BuildEvidence.Signals) != 1 || snap.BuildEvidence.Signals[0] != "Cargo.toml" {
			t.Errorf("cycle %d signals = %v, want [Cargo.toml]", cycle, snap.BuildEvidence.Signals)
		}
	}
}

// TestRunAutoWithRuntime_CodexMeetsTheSameGate is the §2.6 row the ADR chose to
// STATE ("by construction, not by intention") rather than derive. A stated
// property with no case is one a runtime-specific early return can break
// silently — and the reason for the gate reads differently on Codex (a
// filesystem sandbox, not a tool allowlist), which is precisely the argument
// someone would use when adding that return.
func TestRunAutoWithRuntime_CodexMeetsTheSameGate(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t, "mix.exs")
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
	})

	var err error
	captureStdout(t, func() {
		err = runAutoWithRuntime(runner.RuntimeCodex, []string{"add a README section",
			"--no-agent-mapping", "--no-skill-activation"}, fake, browser.NewFakeOpener(), os.Stdout)
	})

	var refusal *coordinator.MissingBuildEvidenceError
	if !errors.As(err, &refusal) {
		t.Fatalf("codex err = %v (%T), want the same refusal Claude gets", err, err)
	}
	if len(refusal.Signals) != 1 || refusal.Signals[0].File != "mix.exs" {
		t.Errorf("the codex refusal names %+v, want the detected mix.exs", refusal.Signals)
	}
	if n := len(fake.Invocations()); n != 0 {
		t.Errorf("the refused codex invocation made %d call(s); a refusal must spend nothing", n)
	}
}

// TestDetectBuildSignals_ThisPackageDirectoryRaisesNone pins the one hazard the
// gate's placement INHERITED from the placement §2.1 rejected. The production
// call site passes "." (main.go, runAutoWithRuntime), so every `auto` test in
// this package that does NOT call inBuildDir passes only because
// cmd/oh-my-graph/ happens to hold no build marker — the same "passing today by
// accident" §2.1 held against putting the gate in autoFlags.parse.
//
// Drop a Makefile, a stray go.mod or a *.csproj fixture in here and dozens of
// unrelated tests fail with a message about build evidence. This converts that
// cascade into one honest failure that says what to do about it.
func TestDetectBuildSignals_ThisPackageDirectoryRaisesNone(t *testing.T) {
	if signals := coordinator.DetectBuildSignals("."); len(signals) != 0 {
		files := make([]string, 0, len(signals))
		for _, s := range signals {
			files = append(files, s.File)
		}
		t.Fatalf("cmd/oh-my-graph/ now holds build marker(s) %v. The gate scans \".\", so every auto test "+
			"here that does not call inBuildDir is now refused for want of build evidence. Either move the "+
			"file out of the package directory, or give those tests a temp directory of their own.", files)
	}
}
