package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/conventions"
	"github.com/jitokim/oh-my-graph/internal/interview"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// ADR 0044: `auto --interview` asks the operator before planning and hands the
// answers to the planner as fenced text. These tests drive the real `auto` and
// `resume` paths against a FakeRunner, with stdin injected as a strings.Reader
// behind a fixed terminal answer — no real terminal and no real model — and are
// numbered after ADR 0044 §5.

// interviewHeaderLead opens every non-empty interview prefix (interview.Render).
const interviewHeaderLead = "Before planning, the person who launched this run was asked the questions"

// interviewAnswer is unique enough that finding it anywhere is a finding.
const interviewAnswer = "ANSWER-7f3e: only the main branch, never a release branch"

const interviewQuestion = "QUESTION: Which branch should the change land on?"

// isInterviewerCall tells the interviewer's prompt from every other call's.
func isInterviewerCall(prompt string) bool {
	return strings.HasPrefix(prompt, "You are interviewing the person who launched oh-my-graph")
}

// newInterviewFake is newConventionsFake with the interviewer's calls keyed
// ask-1, ask-2, … ahead of everything else. A planner prompt carrying the
// interview prefix still contains "planning coordinator", so the planner keys
// as plan-N either way.
func newInterviewFake(outcomes map[string]runner.NodeOutcome) *runner.FakeRunner {
	fake := runner.NewFakeRunner(outcomes)
	askCalls, planCalls, assessCalls, nodeCalls := 0, 0, 0, 0
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		switch {
		case isInterviewerCall(spec.Prompt):
			askCalls++
			return fmt.Sprintf("ask-%d", askCalls)
		case strings.Contains(spec.Prompt, "planning coordinator"):
			planCalls++
			return fmt.Sprintf("plan-%d", planCalls)
		case strings.Contains(spec.Prompt, "goal assessor"):
			assessCalls++
			return fmt.Sprintf("assess-%d", assessCalls)
		}
		nodeCalls++
		return fmt.Sprintf("%s-%d", strings.SplitN(withoutConventions(spec.Prompt), "\n\n", 2)[0], nodeCalls)
	}
	return fake
}

// terminalStdin is the injected stdin: text to read, and what the terminal
// check answers.
func terminalStdin(text string, tty bool) terminalInput {
	return terminalInput{r: strings.NewReader(text), isTerminal: func() bool { return tty }}
}

// runInterviewAuto runs `auto` through the real argv path with stdin injected.
// Mapping and activation are off so no part of the real ~/.claude is read.
func runInterviewAuto(t *testing.T, fake *runner.FakeRunner, stdin terminalInput, args ...string) (string, error) {
	t.Helper()
	args = append(args, "--accept-no-build-evidence", "--no-agent-mapping", "--no-skill-activation")
	var err error
	out := captureStdout(t, func() {
		err = runAutoWithRuntime(runner.RuntimeClaude, args, fake, browser.NewFakeOpener(), os.Stdout, stdin)
	})
	return out, err
}

func callsWhere(fake *runner.FakeRunner, match func(string) bool) []runner.NodeInvocation {
	var calls []runner.NodeInvocation
	for _, spec := range fake.Invocations() {
		if match(spec.Prompt) {
			calls = append(calls, spec)
		}
	}
	return calls
}

func plannerCalls(fake *runner.FakeRunner) []runner.NodeInvocation {
	return callsWhere(fake, func(p string) bool { return !isInterviewerCall(p) && strings.Contains(p, "planning coordinator") })
}

func interviewerCalls(fake *runner.FakeRunner) []runner.NodeInvocation {
	return callsWhere(fake, isInterviewerCall)
}

func sha256OfFile(t *testing.T, path string) (string, []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), data
}

// stateInterview reads one run's state.json raw, and its interview block.
func stateInterview(t *testing.T, runID string) ([]byte, map[string]json.RawMessage) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(runDirFor(runID), stateFileName))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	var head struct {
		Interview map[string]json.RawMessage `json:"interview"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		t.Fatalf("decode state.json: %v", err)
	}
	return raw, head.Interview
}

func runIDs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(runsRoot())
	if err != nil {
		t.Fatalf("read runs root: %v", err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.Name())
	}
	return ids
}

// ADR 0044 §5 test 2
func TestAutoInterview_NoTerminalRefusesBeforeAnyModelCall(t *testing.T) {
	conventionsHome(t)
	fake := newInterviewFake(nil)
	stdin := terminalStdin(interviewAnswer+"\n", false)

	_, err := runInterviewAuto(t, fake, stdin, "tidy the docs", "--interview")
	if !errors.Is(err, errInterviewNeedsTerminal) {
		t.Fatalf("want the no-terminal refusal, got %v", err)
	}
	for _, needle := range []string{"--interview", "drop --interview, or run it from a terminal"} {
		if !strings.Contains(err.Error(), needle) {
			t.Errorf("the refusal lacks %q: %v", needle, err)
		}
	}
	if n := len(fake.Invocations()); n != 0 {
		t.Errorf("the refusal came after %d model call(s); it must come before any", n)
	}
	if n := stdin.r.(*strings.Reader).Len(); n != len(interviewAnswer)+1 {
		t.Errorf("the refusal read stdin (%d bytes left)", n)
	}
	for _, dir := range []string{runsRoot(), filepath.Join(omgHome(), "plans")} {
		if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s exists after the refusal (stat err %v)", dir, err)
		}
	}
}

// ADR 0044 §5 test 4
func TestAutoInterview_PlanOnlyPlansOnceWithThePrefixAndKeepsTheAnswersBesideTheSpec(t *testing.T) {
	conventionsHome(t)
	fake := newInterviewFake(map[string]runner.NodeOutcome{
		"ask-1":  {Result: interviewQuestion, TotalCostUSD: 0.01},
		"ask-2":  {Result: "ENOUGH", TotalCostUSD: 0.01},
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.04},
	})

	out, err := runInterviewAuto(t, fake, terminalStdin(interviewAnswer+"\n", true), "tidy the docs", "--plan-only", "--interview")
	if err != nil {
		t.Fatalf("auto --plan-only --interview: %v", err)
	}
	planners := plannerCalls(fake)
	if len(planners) != 1 {
		t.Fatalf("planner calls = %d, want exactly one", len(planners))
	}
	if n := len(fake.Invocations()) - len(planners) - len(interviewerCalls(fake)); n != 0 {
		t.Errorf("%d call(s) were neither the interviewer nor the planner: a node ran", n)
	}

	planDir := solePlanDir(t)
	_, staged := sha256OfFile(t, filepath.Join(planDir, interview.StagedFileName))
	if !strings.HasPrefix(string(staged), interviewHeaderLead) || !strings.Contains(string(staged), interviewAnswer) {
		t.Fatalf("plans/<id>/%s does not hold the answers:\n%s", interview.StagedFileName, staged)
	}
	if !strings.HasPrefix(planners[0].Prompt, string(staged)) {
		t.Errorf("the planner prompt does not open with the staged prefix:\n%s", firstLine(planners[0].Prompt))
	}
	if _, err := os.Stat(filepath.Join(planDir, generatedSpecFileName)); err != nil {
		t.Errorf("the spec is not beside the answers: %v", err)
	}
	if _, err := os.Stat(runsRoot()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("--plan-only created runs/ (stat err %v)", err)
	}
	if want := filepath.Join(planDir, interview.StagedFileName); !strings.Contains(out, want) {
		t.Errorf("the preview does not say where the answers are kept (%s):\n%s", want, out)
	}
	// Printed once, before the planner call, so the operator saw what it got.
	if strings.Count(out, interviewAnswer) != 1 {
		t.Errorf("the prefix was printed %d time(s), want once:\n%s", strings.Count(out, interviewAnswer), out)
	}
}

// ADR 0044 §5 test 5
func TestAutoInterview_DoneAtTheFirstPromptLeavesThePlannerPromptUnchanged(t *testing.T) {
	outcomes := map[string]runner.NodeOutcome{
		"ask-1":  {Result: interviewQuestion, TotalCostUSD: 0.01},
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.04},
		"work-1": {SessionID: "s-work", Result: "PASS"},
	}

	conventionsHome(t)
	without := newInterviewFake(outcomes)
	if _, err := runInterviewAuto(t, without, terminalStdin("", false), "tidy the docs"); err != nil {
		t.Fatalf("auto without --interview: %v", err)
	}

	conventionsHome(t)
	with := newInterviewFake(outcomes)
	out, err := runInterviewAuto(t, with, terminalStdin("/done\n", true), "tidy the docs", "--interview")
	if err != nil {
		t.Fatalf("auto --interview with /done first: %v", err)
	}
	if n := len(interviewerCalls(with)); n != 1 {
		t.Errorf("interviewer calls = %d, want 1: /done must end the interview", n)
	}
	a, b := plannerCalls(without), plannerCalls(with)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("planner calls = %d and %d, want 1 each", len(a), len(b))
	}
	if a[0].Prompt != b[0].Prompt {
		t.Errorf("/done at the first prompt changed the planner prompt:\nwithout:\n%s\nwith:\n%s", a[0].Prompt, b[0].Prompt)
	}
	if !strings.Contains(out, "exactly what it would be without --interview") {
		t.Errorf("the screen does not say the planner prompt is unchanged:\n%s", out)
	}

	// The record still says an interview was asked and how it ended.
	runID := soleRunID(t)
	sha, staged := sha256OfFile(t, filepath.Join(runDirFor(runID), interview.StagedFileName))
	if len(staged) != 0 {
		t.Errorf("a zero-answer interview staged %q, want an empty file", staged)
	}
	snap, err := runstate.Load(filepath.Join(runDirFor(runID), stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	want := runstate.Interview{StagedSHA256: sha, Asked: 1, Ending: string(interview.EndOperator), CostUSD: 0.01}
	if snap.Interview == nil || *snap.Interview != want {
		t.Errorf("interview record = %+v, want %+v", snap.Interview, want)
	}
}

// ADR 0044 §5 test 12
func TestAutoInterview_GoalLoopAsksOnceAndEveryCycleCarriesTheSamePrefix(t *testing.T) {
	conventionsHome(t)
	fake := newInterviewFake(map[string]runner.NodeOutcome{
		"ask-1":    {Result: interviewQuestion, TotalCostUSD: 0.25, Usage: runner.TokenUsage{InputTokens: 100, OutputTokens: 10}},
		"ask-2":    {Result: "ENOUGH", TotalCostUSD: 0.25, Usage: runner.TokenUsage{InputTokens: 120, OutputTokens: 2}},
		"plan-1":   {Result: cycleSpec, TotalCostUSD: 0.04},
		"work-1":   {SessionID: "s1", Result: "PASS"},
		"assess-1": {Result: cycleAssessNotMet, TotalCostUSD: 0.01},
		"plan-2":   {Result: cycleSpec, TotalCostUSD: 0.04},
		"work-2":   {SessionID: "s2", Result: "PASS"},
		"assess-2": {Result: cycleAssessMet, TotalCostUSD: 0.01},
	})

	out, err := runInterviewAuto(t, fake, terminalStdin(interviewAnswer+"\n", true), "tidy the docs", "--interview", "--max-cycles", "2")
	if err != nil {
		t.Fatalf("auto --interview --max-cycles 2: %v", err)
	}

	// The interviewer is asked before cycle 1 only: every one of its calls
	// precedes the first planner call.
	calls := fake.Invocations()
	firstPlan := -1
	for i, spec := range calls {
		if isInterviewerCall(spec.Prompt) {
			if firstPlan >= 0 {
				t.Errorf("call %d is an interviewer call after planning began", i)
			}
		} else if firstPlan < 0 {
			firstPlan = i
		}
	}
	if n := len(interviewerCalls(fake)); n != 2 {
		t.Errorf("interviewer calls = %d, want 2", n)
	}

	planners := plannerCalls(fake)
	if len(planners) != 2 {
		t.Fatalf("planner calls = %d, want 2", len(planners))
	}
	ids := runIDs(t)
	if len(ids) != 2 {
		t.Fatalf("run directories = %d, want 2", len(ids))
	}
	_, first := sha256OfFile(t, filepath.Join(runDirFor(ids[0]), interview.StagedFileName))
	_, second := sha256OfFile(t, filepath.Join(runDirFor(ids[1]), interview.StagedFileName))
	if string(first) != string(second) || !strings.Contains(string(first), interviewAnswer) {
		t.Fatalf("the two cycles staged different interview files:\n%s\n---\n%s", first, second)
	}
	for i, p := range planners {
		if !strings.HasPrefix(p.Prompt, string(first)) {
			t.Errorf("cycle %d's planner prompt does not open with the staged prefix", i+1)
		}
	}

	// The interview's 0.50 is in cycle 1's planning, and so in the goal's spend.
	snaps := goalSnapshots(t)
	if got := snaps[0].PlanningCostUSD; math.Abs(got-0.54) > 1e-9 {
		t.Errorf("cycle 1 planning cost = %v, want 0.54 (planner 0.04 + interview 0.50)", got)
	}
	if got := snaps[0].PlanningUsage.InputTokens; got != 220 {
		t.Errorf("cycle 1 planning input tokens = %d, want the interview's 220", got)
	}
	if got := snaps[1].PlanningCostUSD; math.Abs(got-0.04) > 1e-9 {
		t.Errorf("cycle 2 planning cost = %v, want 0.04: the interview is counted once", got)
	}
	for _, needle := range []string{
		"interview (before cycle 1): 1 asked, 1 answered, ended enough, cost $0.5000",
		"GOAL TOTAL: $0.6000 across 2 cycle(s)",
	} {
		if !strings.Contains(out, needle) {
			t.Errorf("goal summary lacks %q:\n%s", needle, out)
		}
	}
}

// TestAutoInterview_TheGoalBudgetSeesTheInterview is test 12's last clause
// read through --max-goal-budget-usd: cycle 1's planner, node and assessor
// cost 0.05 against a 0.40 ceiling, so only the interview's 0.50 can stop the
// loop before cycle 2.
func TestAutoInterview_TheGoalBudgetSeesTheInterview(t *testing.T) {
	conventionsHome(t)
	fake := newInterviewFake(map[string]runner.NodeOutcome{
		"ask-1":    {Result: interviewQuestion, TotalCostUSD: 0.25},
		"ask-2":    {Result: "ENOUGH", TotalCostUSD: 0.25},
		"plan-1":   {Result: cycleSpec, TotalCostUSD: 0.04},
		"work-1":   {SessionID: "s1", Result: "PASS"},
		"assess-1": {Result: cycleAssessNotMet, TotalCostUSD: 0.01},
	})
	_, err := runInterviewAuto(t, fake, terminalStdin(interviewAnswer+"\n", true),
		"tidy the docs", "--interview", "--max-cycles", "2", "--max-goal-budget-usd", "0.40")
	if err == nil || !strings.Contains(err.Error(), "goal budget ceiling was reached after cycle 1") {
		t.Fatalf("want the budget stop after cycle 1, got %v", err)
	}
	if n := len(plannerCalls(fake)); n != 1 {
		t.Errorf("planner calls = %d, want 1: the ceiling must stop cycle 2", n)
	}
}

// ADR 0044 §5 test 14
func TestAutoInterview_WithConventionsEachPrefixReachesOnlyItsOwnCall(t *testing.T) {
	conventionsHome(t)
	style, mixed := conventionFiles(t)
	set, err := conventions.Load([]string{style, mixed})
	if err != nil {
		t.Fatal(err)
	}
	fake := newInterviewFake(map[string]runner.NodeOutcome{
		"ask-1":  {Result: interviewQuestion, TotalCostUSD: 0.01},
		"ask-2":  {Result: "ENOUGH", TotalCostUSD: 0.01},
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.04},
		"work-1": {SessionID: "s-work", Result: "PASS"},
	})
	if _, err := runInterviewAuto(t, fake, terminalStdin(interviewAnswer+"\n", true),
		"tidy the docs", "--interview", "--conventions", style, "--conventions", mixed); err != nil {
		t.Fatalf("auto --interview --conventions: %v", err)
	}

	runID := soleRunID(t)
	_, stagedInterview := sha256OfFile(t, filepath.Join(runDirFor(runID), interview.StagedFileName))
	_, stagedConventions := sha256OfFile(t, filepath.Join(runDirFor(runID), conventions.StagedFileName))
	if string(stagedConventions) != string(set.Staged) || !strings.Contains(string(stagedInterview), interviewAnswer) {
		t.Fatal("the run did not stage both prefixes")
	}

	planners := plannerCalls(fake)
	if len(planners) != 1 {
		t.Fatalf("planner calls = %d, want 1", len(planners))
	}
	plannerPrompt := planners[0].Prompt
	if !strings.HasPrefix(plannerPrompt, string(stagedInterview)) {
		t.Error("the planner prompt does not open with the interview prefix")
	}
	if strings.Contains(plannerPrompt, conventionsHeader) {
		t.Error("the planner prompt carries the conventions")
	}

	nodes := nodeInvocations(fake)
	var planned []runner.NodeInvocation
	for _, n := range nodes {
		if !isInterviewerCall(n.Prompt) {
			planned = append(planned, n)
		}
	}
	if len(planned) != 1 {
		t.Fatalf("node spawns = %d, want 1", len(planned))
	}
	if want := string(set.Staged) + "work"; planned[0].Prompt != want {
		t.Errorf("node prompt:\n%q\nwant the conventions and the node's own prompt only:\n%q", planned[0].Prompt, want)
	}
	if strings.Contains(planned[0].Prompt, interviewAnswer) || strings.Contains(planned[0].Prompt, interviewHeaderLead) {
		t.Error("the interview reached a planned node's prompt")
	}
}

// ADR 0044 §5 test 16
func TestAutoInterview_StateJSONRecordsTheInterviewButNotItsText(t *testing.T) {
	outcomes := map[string]runner.NodeOutcome{
		"ask-1":  {Result: interviewQuestion, TotalCostUSD: 0.02, Usage: runner.TokenUsage{InputTokens: 50, OutputTokens: 5}},
		"ask-2":  {Result: "QUESTION: Should the old docs be deleted or archived?", TotalCostUSD: 0.03},
		"ask-3":  {Result: "QUESTION: Who reviews the result?", TotalCostUSD: 0.01},
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.04},
		"work-1": {SessionID: "s-work", Result: "PASS"},
	}

	conventionsHome(t)
	fake := newInterviewFake(outcomes)
	// One answer, one skip, then end of input at the third question: the
	// counts must keep all three apart.
	if _, err := runInterviewAuto(t, fake, terminalStdin(interviewAnswer+"\n/skip\n", true), "tidy the docs", "--interview"); err != nil {
		t.Fatalf("auto --interview: %v", err)
	}
	runID := soleRunID(t)
	sha, staged := sha256OfFile(t, filepath.Join(runDirFor(runID), interview.StagedFileName))
	raw, block := stateInterview(t, runID)

	wantKeys := []string{"answered", "asked", "cost_usd", "ending", "skipped", "staged_sha256", "usage"}
	var gotKeys []string
	for k := range block {
		gotKeys = append(gotKeys, k)
	}
	if !sameStrings(gotKeys, wantKeys) {
		t.Errorf("interview keys = %v, want %v", gotKeys, wantKeys)
	}
	var record runstate.Interview
	if err := json.Unmarshal(mustMarshal(t, block), &record); err != nil {
		t.Fatal(err)
	}
	want := runstate.Interview{
		StagedSHA256: sha, Asked: 3, Answered: 1, Skipped: 1, Ending: string(interview.EndEOF),
		Usage: runstate.TokenUsage{InputTokens: 50, OutputTokens: 5},
	}
	want.CostUSD = record.CostUSD
	if record != want || math.Abs(record.CostUSD-0.06) > 1e-9 {
		t.Errorf("interview record = %+v, want %+v with cost 0.06", record, want)
	}
	for _, text := range []string{interviewAnswer, "Which branch should the change land on", "deleted or archived", "Who reviews", interviewHeaderLead} {
		if strings.Contains(string(raw), text) {
			t.Errorf("state.json carries interview text %q", text)
		}
	}
	if !strings.Contains(string(staged), interviewAnswer) {
		t.Error("the staged file does not hold the answer the record hashes")
	}

	// Without the flag: no block, no staged file.
	conventionsHome(t)
	plain := newInterviewFake(outcomes)
	if _, err := runInterviewAuto(t, plain, terminalStdin("", true), "tidy the docs"); err != nil {
		t.Fatalf("auto: %v", err)
	}
	plainID := soleRunID(t)
	if _, block := stateInterview(t, plainID); block != nil {
		t.Errorf("a run without --interview recorded %v", block)
	}
	if _, err := os.Stat(filepath.Join(runDirFor(plainID), interview.StagedFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a run without --interview staged %s (stat err %v)", interview.StagedFileName, err)
	}
	if n := len(interviewerCalls(plain)); n != 0 {
		t.Errorf("a run without --interview asked the interviewer %d time(s)", n)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int)
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
