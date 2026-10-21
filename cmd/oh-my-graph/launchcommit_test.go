package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// launchRepo makes a git repository with one commit in a temp dir and returns
// the dir and `git rev-parse HEAD` — the value launchCommit must reproduce
// through the worktree seam (#414).
func launchRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "main")
	launchCommitWork(t, dir, "README.md")
	return dir, mustGit(t, dir, "rev-parse", "HEAD")
}

// launchCommitWork writes name in dir and commits it under a fixed identity.
func launchCommitWork(t *testing.T, dir, name string) {
	t.Helper()
	if err := commitFile(dir, name); err != nil {
		t.Fatal(err)
	}
}

// commitFile is launchCommitWork returning its error, for the fake runner's
// goroutines, where a test may not be failed.
func commitFile(dir, name string) error {
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
		return err
	}
	if _, err := gitOutput(dir, "add", "--", name); err != nil {
		return err
	}
	_, err := gitOutput(dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false",
		"commit", "-q", "-m", "add "+name)
	return err
}

func TestLaunchCommit_MatchesGitRevParseHEAD(t *testing.T) {
	dir, want := launchRepo(t)
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, at := range map[string]string{"root": dir, "subdirectory": sub} {
		if got := launchCommit(at); got != want {
			t.Errorf("%s: launchCommit = %q, want %q", name, got, want)
		}
	}
}

func TestLaunchCommit_EmptyOutsideARepository(t *testing.T) {
	if got := launchCommit(t.TempDir()); got != "" {
		t.Errorf("launchCommit outside a repository = %q, want \"\"", got)
	}
	if got := launchCommit(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Errorf("launchCommit on a missing dir = %q, want \"\"", got)
	}
}

func TestLaunchCommit_DetachedHEAD(t *testing.T) {
	dir, want := launchRepo(t)
	mustGit(t, dir, "checkout", "-q", "--detach")
	if got := launchCommit(dir); got != want {
		t.Errorf("detached: launchCommit = %q, want %q", got, want)
	}
}

// A linked worktree reports its own HEAD, not the main checkout's.
func TestLaunchCommit_LinkedWorktree(t *testing.T) {
	dir, _ := launchRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	mustGit(t, dir, "worktree", "add", "-q", "-b", "side", wt)
	launchCommitWork(t, wt, "side.txt")
	want := mustGit(t, wt, "rev-parse", "HEAD")
	if got := launchCommit(wt); got != want {
		t.Errorf("worktree: launchCommit = %q, want %q", got, want)
	}
	if got := launchCommit(dir); got == want {
		t.Error("the main checkout reports the worktree's HEAD")
	}
}

// #414: `auto` reads HEAD once at launch, and every cycle's planner prompt
// carries that commit — even after cycle 1's node has moved HEAD.
func TestRunAuto_EveryCyclePlansFromTheLaunchCommit(t *testing.T) {
	isolateRunHome(t)
	dir, launch := launchRepo(t)
	t.Chdir(dir)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec},
		"work-1":   {SessionID: "s-work-1", Result: "PASS"},
		"assess-1": {Result: cycleAssessNotMet},
		"plan-2":   {Result: cycleSpec},
		"work-2":   {SessionID: "s-work-2", Result: "PASS"},
		"assess-2": {Result: cycleAssessMet},
	})
	key := fake.KeyFn
	var moved string
	var moveErr error
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		k := key(spec)
		if k == "work-1" {
			moveErr = commitFile(dir, "cycle1.txt")
			moved = launchCommit(dir)
		}
		return k
	}

	if out, err := runBaselineAuto(t, fake, verify.NewFakeVerifier(nil), osStdin(), "add a README section", "--max-cycles", "2"); err != nil {
		t.Fatalf("auto: %v\n%s", err, out)
	}
	if moveErr != nil {
		t.Fatalf("fixture: %v", moveErr)
	}
	if moved == "" || moved == launch {
		t.Fatalf("fixture: cycle 1 did not move HEAD (launch %s, after %s)", launch, moved)
	}
	invocations := fake.Invocations()
	planned := 0
	for i, k := range fake.Calls() {
		if !strings.HasPrefix(k, "plan-") {
			continue
		}
		planned++
		prompt := invocations[i].Prompt
		if !strings.Contains(prompt, launch+"..HEAD") {
			t.Errorf("%s does not measure from the launch commit %s", k, launch)
		}
		if strings.Contains(prompt, moved) {
			t.Errorf("%s names the HEAD cycle 1 moved to (%s); it must keep the launch commit", k, moved)
		}
	}
	if planned != 2 {
		t.Fatalf("%d planner call(s), want 2", planned)
	}
}

// #414: `design` hands the planner the commit it launched on too.
func TestDesign_PlansFromTheLaunchCommit(t *testing.T) {
	conventionsHome(t)
	dir, launch := launchRepo(t)
	t.Chdir(dir)
	fake := newInterviewFake(designOutcomes())
	path := filepath.Join(t.TempDir(), "designed.yaml")

	if out, _, err := runDesign(t, fake, terminalStdin(interviewAnswer+"\n", true), nil, "tidy the docs", "--out", path); err != nil {
		t.Fatalf("design: %v\n%s", err, out)
	}
	calls := plannerCalls(fake)
	if len(calls) != 1 {
		t.Fatalf("%d planner call(s), want 1", len(calls))
	}
	if !strings.Contains(calls[0].Prompt, launch+"..HEAD") {
		t.Errorf("design's planner prompt does not measure from the launch commit %s", launch)
	}
}

// Outside a repository auto and design pass WithStartCommit(""), which adds
// no paragraph: the prompt before #414.
func TestLaunchCommitOfWorkingDir_EmptyOutsideARepository(t *testing.T) {
	t.Chdir(t.TempDir())
	if got := launchCommitOfWorkingDir(); got != "" {
		t.Errorf("launchCommitOfWorkingDir = %q, want \"\"", got)
	}
}

// #414: `chat` reads HEAD once at launch, and every plan of the session
// carries that commit — even after turn 1's run has moved HEAD.
func TestRunChatWith_EveryPlanOfTheSessionScopesFromTheLaunchCommit(t *testing.T) {
	conventionsHome(t)
	dir, launch := launchRepo(t)
	t.Chdir(dir)
	fake := newChatFake(map[string]runner.NodeOutcome{
		routerKey:   {Result: `{"mode":"graph","goal":"add a README section"}`},
		autoPlanKey: {Result: cycleSpec},
		"work":      {SessionID: "s-work", Result: "PASS"},
	})
	key := fake.KeyFn
	var moved string
	var moveErr error
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		k := key(spec)
		if k == "work" && moved == "" {
			moveErr = commitFile(dir, "turn1.txt")
			moved = launchCommit(dir)
		}
		return k
	}

	var out strings.Builder
	var chatErr error
	captureStdout(t, func() {
		chatErr = runChatWith(runner.RuntimeClaude, []string{"--no-agent-mapping", "--no-skill-activation"},
			strings.NewReader("add a README section\ny\nadd another section\ny\n"), &out, fake)
	})
	if chatErr != nil {
		t.Fatalf("chat: %v\n%s", chatErr, out.String())
	}
	if moveErr != nil {
		t.Fatalf("fixture: %v", moveErr)
	}
	if moved == "" || moved == launch {
		t.Fatalf("fixture: turn 1 did not move HEAD (launch %s, after %s)", launch, moved)
	}
	invocations := fake.Invocations()
	planned := 0
	for i, k := range fake.Calls() {
		if k != autoPlanKey {
			continue
		}
		planned++
		prompt := invocations[i].Prompt
		if !strings.Contains(prompt, launch+"..HEAD") {
			t.Errorf("plan %d does not measure from the launch commit %s", planned, launch)
		}
		if strings.Contains(prompt, moved) {
			t.Errorf("plan %d names the HEAD turn 1 moved to (%s); it must keep the launch commit", planned, moved)
		}
	}
	if planned != 2 {
		t.Fatalf("%d planner call(s), want 2", planned)
	}
}
