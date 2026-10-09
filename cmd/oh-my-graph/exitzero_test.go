package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// The CLI half of #371: the coordinator pairs every planned `result_matches`
// with `exit_zero` after validation and records the ids in
// Plan.ExitZeroAdded; these tests pin that the plan screen says so, that the
// saved artifacts carry the guard, and that a hand-written `run` graph is left
// alone.

const (
	exitZeroAddedLine = "  note: node review: exit_zero: true added beside its result_matches (a planned check is never weaker than the exit code)\n"
	// exitZeroReviewPattern is the review node's verdict, which must come out
	// of every artifact exactly as the planner wrote it.
	exitZeroReviewPattern = "^APPROVED"
	uncheckedVerdictLint  = "result_matches without exit_zero"
)

// unpairedVerdictSpec is a typical planned graph from before #371's prompt
// fix: work, then a review whose verdict is result_matches ALONE.
const unpairedVerdictSpec = `{"name":"review-work","version":"1","nodes":[` +
	`{"id":"implement","prompt":"implement it","allowed_tools":["Read","Edit"]},` +
	`{"id":"review","depends_on":["implement"],"prompt":"review it. START your reply with APPROVED or REJECTED.","allowed_tools":["Read"],` +
	`"success_check":{"result_matches":"` + exitZeroReviewPattern + `"}}]}`

// pairedVerdictSpec needs nothing added: one node with no success_check, and
// the review already pairing its verdict with exit_zero.
const pairedVerdictSpec = `{"name":"review-work","version":"1","nodes":[` +
	`{"id":"implement","prompt":"implement it","allowed_tools":["Read","Edit"]},` +
	`{"id":"review","depends_on":["implement"],"prompt":"review it. START your reply with APPROVED or REJECTED.","allowed_tools":["Read"],` +
	`"success_check":{"exit_zero":true,"result_matches":"` + exitZeroReviewPattern + `"}}]}`

// newExitZeroFake keys the planner call as plan-N and every node by the first
// line of its prompt, and passes both nodes of the specs above.
func newExitZeroFake(spec string) *runner.FakeRunner {
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"plan-1":       {Result: spec, TotalCostUSD: 0.04},
		"implement it": {SessionID: "s-implement", Result: "done", ExitCode: 0},
		"review it. START your reply with APPROVED or REJECTED.": {SessionID: "s-review", Result: "APPROVED", ExitCode: 0},
	})
	planCalls := 0
	fake.KeyFn = func(inv runner.NodeInvocation) string {
		if strings.Contains(inv.Prompt, "planning coordinator") {
			planCalls++
			return fmt.Sprintf("plan-%d", planCalls)
		}
		return firstLine(inv.Prompt)
	}
	return fake
}

// runExitZeroAuto launches `auto` through the real argv path with extra flags
// and returns everything it printed on stdout and stderr.
func runExitZeroAuto(t *testing.T, spec string, extra ...string) string {
	t.Helper()
	fake := newExitZeroFake(spec)
	args := append([]string{"review the work", "--no-agent-mapping", "--no-skill-activation"}, extra...)
	var stdout string
	stderr, err := captureStderr(t, func() error {
		var runErr error
		stdout = captureStdout(t, func() {
			runErr = runAutoWith(args, fake, browser.NewFakeOpener(), os.Stdout)
		})
		return runErr
	})
	if err != nil {
		t.Fatalf("auto %v: %v\n%s%s", extra, err, stdout, stderr)
	}
	return stdout + stderr
}

// specCheckOf returns node id's success_check as the JSON bytes spell it, so
// an assertion reads the file rather than a value a decoder defaulted.
func specCheckOf(t *testing.T, spec []byte, id string) map[string]json.RawMessage {
	t.Helper()
	var raw struct {
		Nodes []struct {
			ID           string                     `json:"id"`
			SuccessCheck map[string]json.RawMessage `json:"success_check"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(spec, &raw); err != nil {
		t.Fatalf("spec is not JSON: %v\n%s", err, spec)
	}
	for _, n := range raw.Nodes {
		if n.ID == id {
			return n.SuccessCheck
		}
	}
	t.Fatalf("spec lost node %q:\n%s", id, spec)
	return nil
}

// assertPairedReview checks the review node of a saved JSON spec carries
// exit_zero true with its result_matches unchanged.
func assertPairedReview(t *testing.T, what string, spec []byte) {
	t.Helper()
	check := specCheckOf(t, spec, "review")
	if got := string(check["exit_zero"]); got != "true" {
		t.Errorf("%s: review exit_zero = %q, want true:\n%s", what, got, spec)
	}
	if got := string(check["result_matches"]); got != `"`+exitZeroReviewPattern+`"` {
		t.Errorf("%s: review result_matches = %s, want %q unchanged", what, got, exitZeroReviewPattern)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// --- unit: the helper ------------------------------------------------------

// #371: one line per node, and nothing at all for an empty list.
func TestNoteExitZeroAdded(t *testing.T) {
	var none bytes.Buffer
	noteExitZeroAdded(&none, nil)
	if none.Len() != 0 {
		t.Errorf("an empty list printed %q, want nothing", none.String())
	}

	var two bytes.Buffer
	noteExitZeroAdded(&two, []string{"review", "check"})
	want := exitZeroAddedLine + strings.Replace(exitZeroAddedLine, "node review:", "node check:", 1)
	if two.String() != want {
		t.Errorf("noteExitZeroAdded =\n%s\nwant\n%s", two.String(), want)
	}
}

// --- (a) the added check is shown and saved --------------------------------

// #371 (a): --plan-only on a review with result_matches alone names the node
// on the plan screen, and both saved copies — graph.json and graph.yaml —
// carry exit_zero true with the pattern unchanged.
func TestRunAutoWith_PlanOnlyShowsAndSavesTheAddedExitZero(t *testing.T) {
	isolateRunHome(t)
	out := runExitZeroAuto(t, unpairedVerdictSpec, "--plan-only")

	if !strings.Contains(out, exitZeroAddedLine) {
		t.Errorf("plan screen does not name the node that gained exit_zero:\n%s", out)
	}
	if strings.Contains(out, "node implement: exit_zero") {
		t.Errorf("a node with no success_check was reported as changed:\n%s", out)
	}

	planDir := solePlanDir(t)
	assertPairedReview(t, "plans/<id>/graph.json", readFile(t, filepath.Join(planDir, generatedSpecFileName)))

	yamlPath := filepath.Join(planDir, generatedSpecYAMLFileName)
	fromYAML, err := graph.Load(yamlPath)
	if err != nil {
		t.Fatalf("graph.Load(graph.yaml): %v", err)
	}
	found := false
	for _, n := range fromYAML.Nodes {
		if n.ID != "review" {
			continue
		}
		found = true
		if !n.SuccessCheck.ExitZero || n.SuccessCheck.ResultMatches != exitZeroReviewPattern {
			t.Errorf("graph.yaml review success_check = %+v, want exit_zero true beside %q:\n%s",
				n.SuccessCheck, exitZeroReviewPattern, readFile(t, yamlPath))
		}
	}
	if !found {
		t.Errorf("graph.yaml lost the review node:\n%s", readFile(t, yamlPath))
	}
}

// #371 (a) and (d): a real auto launch saves the paired check into the run's
// graph.json and state.json, shows the line, and the plan screen's verdict
// lint has nothing left to warn about.
func TestRunAutoWith_RunDirectoryCarriesTheAddedExitZero(t *testing.T) {
	isolateRunHome(t)
	out := runExitZeroAuto(t, unpairedVerdictSpec, "--accept-no-build-evidence")

	if !strings.Contains(out, exitZeroAddedLine) {
		t.Errorf("plan screen does not name the node that gained exit_zero:\n%s", out)
	}
	if strings.Contains(out, uncheckedVerdictLint) {
		t.Errorf("a planned graph still drew %q:\n%s", uncheckedVerdictLint, out)
	}

	runDir := runDirFor(soleRunID(t))
	assertPairedReview(t, "runs/<id>/graph.json", readFile(t, filepath.Join(runDir, generatedSpecFileName)))
	snap, err := runstate.Load(filepath.Join(runDir, runstate.SnapshotFileName))
	if err != nil {
		t.Fatalf("load state.json: %v", err)
	}
	assertPairedReview(t, "state.json graph", snap.Graph)
}

// --- (b) nothing added, nothing said ---------------------------------------

// #371 (b): a plan whose verdicts already pair, beside a node with no
// success_check, prints no added-check line at all.
func TestRunAutoWith_PairedPlanPrintsNoExitZeroLine(t *testing.T) {
	isolateRunHome(t)
	out := runExitZeroAuto(t, pairedVerdictSpec, "--plan-only")

	if strings.Contains(out, "exit_zero: true added") {
		t.Errorf("a plan with nothing to add printed an added-check line:\n%s", out)
	}
	if strings.Contains(out, uncheckedVerdictLint) {
		t.Errorf("a paired plan drew %q:\n%s", uncheckedVerdictLint, out)
	}
}

// --- (c) a hand-written graph is the user's own ----------------------------

const handWrittenUnpairedYAML = `name: hand-written
version: "1"
nodes:
  - id: implement
    prompt: implement it
  - id: review
    depends_on: [implement]
    prompt: "review it. START your reply with APPROVED or REJECTED."
    success_check:
      result_matches: "^APPROVED"
`

// #371 (c): `run` rewrites nothing in a hand-written graph — its
// result_matches-only node keeps no exit_zero in the run's state.json, no
// added-check line prints — and `lint` still warns about it exactly once.
func TestRunGraphWith_HandWrittenVerdictIsNotPaired(t *testing.T) {
	isolateRunHome(t)
	path := filepath.Join(t.TempDir(), "graph.yaml")
	writeFileTree(t, path, handWrittenUnpairedYAML)

	fake := newExitZeroFake("")
	var stdout string
	stderr, err := captureStderr(t, func() error {
		var runErr error
		stdout = captureStdout(t, func() {
			runErr = runGraphWith([]string{path}, fake, browser.NewFakeOpener(), os.Stdout)
		})
		return runErr
	})
	if err != nil {
		t.Fatalf("run: %v\n%s%s", err, stdout, stderr)
	}
	if out := stdout + stderr; strings.Contains(out, "exit_zero: true added") {
		t.Errorf("`run` reported adding exit_zero to a hand-written graph:\n%s", out)
	}
	if got := string(readFile(t, path)); got != handWrittenUnpairedYAML {
		t.Errorf("`run` rewrote the graph file:\n%s", got)
	}
	snap, err := runstate.Load(filepath.Join(runDirFor(soleRunID(t)), runstate.SnapshotFileName))
	if err != nil {
		t.Fatalf("load state.json: %v", err)
	}
	check := specCheckOf(t, snap.Graph, "review")
	if v, ok := check["exit_zero"]; ok && string(v) != "false" {
		t.Errorf("hand-written review gained exit_zero = %s in state.json", v)
	}

	var lintOut, lintWarn bytes.Buffer
	if err := lintGraph(&lintOut, &lintWarn, path); err != nil {
		t.Fatalf("lint: %v\n%s", err, lintOut.String())
	}
	warnings := lintWarn.String()
	if n := strings.Count(warnings, uncheckedVerdictLint); n != 1 {
		t.Errorf("lint warned %q %d times, want exactly once:\n%s", uncheckedVerdictLint, n, warnings)
	}
	if !strings.Contains(warnings, `node "review"`) {
		t.Errorf("lint's warning does not name the review node:\n%s", warnings)
	}
}
